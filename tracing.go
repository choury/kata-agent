// Copyright (c) 2018-2019 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0
//

package main

import (
	"context"
	"io"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/uber/jaeger-client-go/config"
)

const (
	jaegerAgentHost = "127.0.0.1"

	// This is the default.
	jaegerAgentPort = "6831"
)

// agentSpan implements opentracing.Span
type agentSpan struct {
	span opentracing.Span
}

// The first trace span
var rootSpan *agentSpan

// Implements jaeger-client-go.Logger interface
type traceLogger struct {
}

func (a *agentSpan) setTag(key string, value interface{}) *agentSpan {
	a.span.SetTag(key, value)
	return a
}

func (a *agentSpan) finish() {
	a.span.Finish()
}

func (a *agentSpan) tracer() agentTracer {
	return agentTracer{tracer: a.span.Tracer()}
}

type agentTracer struct {
	tracer opentracing.Tracer
}

func (a *agentTracer) startSpan(name string) agentSpan {
	return agentSpan{span: a.tracer.StartSpan(name)}
}

func spanFromContext(ctx context.Context) *agentSpan {
	var a agentSpan
	a.span = opentracing.SpanFromContext(ctx)
	return &a
}

func spanStartFromContext(ctx context.Context, name string) (agentSpan, context.Context) {
	var a agentSpan
	a.span, ctx = opentracing.StartSpanFromContext(ctx, name)
	return a, ctx
}

func contextWithSpan(ctx context.Context, a agentSpan) context.Context {
	return opentracing.ContextWithSpan(ctx, a.span)
}

// tracerCloser contains a copy of the closer returned by createTracer() which
// is used by stopTracing().
var tracerCloser io.Closer

func (t traceLogger) Error(msg string) {
	agentLog.Error(msg)
}

func (t traceLogger) Infof(msg string, args ...interface{}) {
	agentLog.Infof(msg, args...)
}

func createTracer(name string) (*agentTracer, error) {
	cfg := &config.Configuration{
		ServiceName: name,

		// If tracing is disabled, use a NOP trace implementation
		Disabled: !tracing,

		// Note that span logging reporter option cannot be enabled as
		// it pollutes the output stream which causes (atleast) the
		// "state" command to fail under Docker.
		Sampler: &config.SamplerConfig{
			Type:  "const",
			Param: 1,
		},

		Reporter: &config.ReporterConfig{
			// Specify the default values since without them,
			// Jaeger will attempt to call the DNS resolver and
			// that will fail since the agent runs relatively
			// early in the boot sequence!
			LocalAgentHostPort: jaegerAgentHost + ":" + jaegerAgentPort,

			// Useful to validate tracing.
			LogSpans: tracing,
		},
	}

	logger := traceLogger{}

	tracer, closer, err := cfg.NewTracer(config.Logger(logger))
	if err != nil {
		return nil, err
	}

	// save for stopTracing()'s exclusive use
	tracerCloser = closer

	// Seems to be essential to ensure non-root spans are logged
	opentracing.SetGlobalTracer(tracer)

	return &agentTracer{tracer: tracer}, nil
}

func setupTracing(rootSpanName string) (*agentSpan, context.Context, error) {
	ctx := context.Background()

	tracer, err := createTracer(agentName)
	if err != nil {
		return nil, nil, err
	}

	// Create the root span (which is .Finish()'d by stopTracing())
	span := tracer.startSpan(rootSpanName)
	span.setTag("source", "agent")
	span.setTag("root-span", "true")

	// See comment in trace().
	if tracing {
		agentLog.Debugf("created root span %v", span)
	}

	// Associate the root span with the context
	ctx = contextWithSpan(ctx, span)

	return &span, ctx, nil
}

// stopTracing() ends all tracing, reporting the spans to the collector.
func stopTracing(ctx context.Context) {
	// Handle scenario where die() is called early in startup
	if ctx == nil {
		return
	}

	if !tracing {
		return
	}

	span := spanFromContext(ctx)
	if span != nil {
		span.finish()
	}

	// report all possible spans to the collector
	tracerCloser.Close()

	tracing = false
	startTracingCalled = false
	stopTracingCalled = false
}

// trace creates a new tracing span based on the specified contex, subsystem
// and name.
func trace(ctx context.Context, subsystem, name string) (*agentSpan, context.Context) {
	span, ctx := spanStartFromContext(ctx, name)

	span.setTag("subsystem", subsystem)

	// This is slightly confusing: when tracing is disabled, trace spans
	// are still created - but the tracer used is a NOP. Therefore, only
	// display the message when tracing is really enabled.
	if tracing {
		agentLog.Debugf("created span %v", span)
	}

	return &span, ctx
}
