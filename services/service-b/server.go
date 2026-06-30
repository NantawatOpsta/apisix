package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	fiberotel "github.com/gofiber/contrib/v3/otel"
	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type userinfo struct {
	PreferredUsername string `json:"preferred_username"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

const (
	localIdentity    = "identity"
	localIDToken     = "id_token"
	localAccessToken = "access_token"
)

func main() {
	shutdown, err := initTracer(context.Background(), "service-b")
	if err != nil {
		log.Fatalf("initTracer: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			log.Printf("tracer shutdown error: %v", err)
		}
	}()

	app := fiber.New(fiber.Config{
		ReadBufferSize: 1 << 20,
	})

	app.Use(fiberotel.Middleware(
		fiberotel.WithPropagators(otel.GetTextMapPropagator()),
		fiberotel.WithTraceResponseHeader("X-Trace-Id"),
	))

	app.Get("/", extractIdentity, handler)

	log.Fatal(app.Listen(":3001"))
}

func initTracer(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	exporter, err := otlptrace.New(ctx, otlptracehttp.NewClient(
		otlptracehttp.WithEndpoint("otel-collector:4318"),
		otlptracehttp.WithInsecure(),
	))
	if err != nil {
		return nil, fmt.Errorf("otlptrace.New: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName(serviceName),
			semconv.ServiceNamespace("tbox"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("resource.Merge: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

func extractIdentity(c fiber.Ctx) error {
	raw := c.Get("X-Userinfo")
	if raw == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("missing identity header")
	}
	// APISIX OIDC plugin base64-encodes X-Userinfo before forwarding upstream.
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).SendString("invalid identity header")
	}
	var ui userinfo
	if err := json.Unmarshal(decoded, &ui); err != nil {
		return c.Status(fiber.StatusUnauthorized).SendString("invalid identity header")
	}
	c.Locals(localIdentity, ui)
	c.Locals(localIDToken, c.Get("X-ID-Token"))
	c.Locals(localAccessToken, c.Get("X-Access-Token"))
	return c.Next()
}

func handler(c fiber.Ctx) error {
	ui := c.Locals(localIdentity).(userinfo)
	idToken := c.Locals(localIDToken).(string)
	accessToken := c.Locals(localAccessToken).(string)

	name := ui.PreferredUsername
	if name == "" {
		name = "user"
	}

	uiJSON, _ := json.MarshalIndent(ui, "", "  ")

	var b strings.Builder
	b.WriteString("<h1>Hello, ")
	b.WriteString(html.EscapeString(name))
	b.WriteString("! (role: tbox-user-service-b)</h1>\n")
	b.WriteString("<h3>X-Userinfo</h3>\n<pre>")
	b.WriteString(html.EscapeString(string(uiJSON)))
	b.WriteString("</pre>\n")
	b.WriteString("<h3>X-ID-Token</h3>\n<div style=\"max-width: 50%;\">")
	b.WriteString(html.EscapeString(idToken))
	b.WriteString("</div>\n")
	b.WriteString("<h3>X-Access-Token</h3>\n<div style=\"max-width: 50%;\">")
	b.WriteString(html.EscapeString(accessToken))
	b.WriteString("</div>\n")

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(b.String())
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s + "..."
}