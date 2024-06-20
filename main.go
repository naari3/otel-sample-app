package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type MultiOp struct {
	Left  int
	Right int
}

type MultiOpResponse struct {
	Result int
}

var tracer = otel.Tracer("github.com/naari3/otel-sample-app")
var serviceName = os.Getenv("SERVICE_NAME")

var apiServerHost = os.Getenv("API_SERVER_HOST")
var port = os.Getenv("PORT")

func initTracer(ctx context.Context) error {
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	)
	if _, present := os.LookupEnv("OTEL_RESOURCE_ATTRIBUTES"); present {
		envResource, err := resource.New(ctx, resource.WithFromEnv())
		if err != nil {
			return err
		}
		// Merge the two resources
		res, err = resource.Merge(res, envResource)
		if err != nil {
			return err
		}
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
	if err != nil {
		return err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return nil
}

func setuplogger() {
	l := slog.New(NewLogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
	})))
	slog.SetDefault(l)
}

func main() {
	setuplogger()
	ctx := context.Background()
	err := initTracer(ctx)
	if err != nil {
		// use slog instead of log to output to stdout
		slog.ErrorContext(ctx, err.Error())
		os.Exit(1)
	}
	r := mux.NewRouter()
	r.Use(otelmux.Middleware(serviceName))

	r.HandleFunc("/", indexHandler).Methods("GET")
	r.HandleFunc("/multi", multiHandler).Methods("GET")

	log.Fatal(http.ListenAndServe(":"+port, r))
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ctx := r.Context()
	_, span := tracer.Start(ctx, "helloHandler")
	defer span.End()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Hello, World!"}`))
}

func multiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ctx := r.Context()
	ctx, span := tracer.Start(ctx, "multiHandler")
	defer span.End()

	// decode request from query parameter
	var multiOp MultiOp
	err := func() error {
		q := r.URL.Query()
		left, err := strconv.Atoi(q.Get("left"))
		if err != nil {
			return err
		}
		right, err := strconv.Atoi(q.Get("right"))
		if err != nil {
			return err
		}
		multiOp = MultiOp{Left: left, Right: right}
		return nil
	}()

	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message": "Invalid request"}`))
		return
	}

	// return result with a 1/30 chance
	if rand.Intn(20) == 0 {
		result := calculateMultiOp(ctx, multiOp)
		response := MultiOpResponse{Result: result}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
		slog.InfoContext(ctx, "Multiplied successfully", "left", multiOp.Left, "right", multiOp.Right, "result", result)
		return
	}

	slog.InfoContext(ctx, "hmmmm, thats a tough one", "left", multiOp.Left, "right", multiOp.Right)

	// return 500 status code with a 1/30 chance
	if rand.Intn(20) == 0 {
		span.AddEvent("Giving up, that was too hard", oteltrace.WithAttributes(attribute.String("reason", "too hard")))
		slog.ErrorContext(ctx, "Giving up, that was too hard")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message": "Internal Server Error"}`))
		return
	}

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	var statusCode int
	var response []byte

	// call API server
	err = func(ctx context.Context) error {
		_, span := tracer.Start(ctx, "callAPI")
		defer span.End()
		req, err := http.NewRequest("GET", "http://"+apiServerHost+"/multi?left="+strconv.Itoa(multiOp.Left)+"&right="+strconv.Itoa(multiOp.Right), nil)
		if err != nil {
			return err
		}

		res, err := client.Do(req)
		if err != nil {
			return err
		}
		statusCode = res.StatusCode
		if statusCode == http.StatusOK {
			response, err = io.ReadAll(res.Body)
			if err != nil {
				return err
			}
		}

		return nil
	}(ctx)
	slog.InfoContext(ctx, "API server called")

	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message": "Internal Server Error", "error": "` + err.Error() + `"}`))
		return
	}
	if statusCode != http.StatusOK {
		slog.ErrorContext(ctx, "API server returned non-200 status code")
		w.WriteHeader(statusCode)
		w.Write([]byte(`{"message": "Internal Server Error", "status_code": ` + strconv.Itoa(statusCode) + `}`))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(response)
}

func calculateMultiOp(ctx context.Context, op MultiOp) int {
	_, span := tracer.Start(ctx, "calculateMultiOp", oteltrace.WithAttributes(attribute.Int("left", op.Left), attribute.Int("right", op.Right)))
	defer span.End()
	result := op.Left * op.Right
	span.SetAttributes(attribute.Int("result", result))
	return op.Left * op.Right
}
