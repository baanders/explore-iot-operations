package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/iot-for-all/device-simulation/components/broker"
	"github.com/iot-for-all/device-simulation/components/client"
	"github.com/iot-for-all/device-simulation/components/edge"
	"github.com/iot-for-all/device-simulation/components/formatter"
	"github.com/iot-for-all/device-simulation/components/limiter"
	"github.com/iot-for-all/device-simulation/components/node"
	"github.com/iot-for-all/device-simulation/components/observer"
	"github.com/iot-for-all/device-simulation/components/outlet"
	"github.com/iot-for-all/device-simulation/components/provider"
	"github.com/iot-for-all/device-simulation/components/publisher"
	"github.com/iot-for-all/device-simulation/components/registry"
	"github.com/iot-for-all/device-simulation/components/renderer"
	"github.com/iot-for-all/device-simulation/components/site"
	"github.com/iot-for-all/device-simulation/components/subscriber"
	"github.com/iot-for-all/device-simulation/components/topic"
	"github.com/iot-for-all/device-simulation/components/tracer"
	"github.com/iot-for-all/device-simulation/lib/env"
	"github.com/iot-for-all/device-simulation/lib/exporter"
	"github.com/iot-for-all/device-simulation/lib/krill"
	"github.com/iot-for-all/device-simulation/lib/logger"
	"gopkg.in/yaml.v3"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

func main() {
	err := run()
	if err != nil {
		panic(err)
	}
}

func run() error {

	fmt.Print(krill.Krill)

	ctx := context.Background()

	reg := prometheus.NewRegistry()

	flagParser := env.NewFlagParser()

	flags, err := flagParser.ReadFlags(map[string]any{
		"config": "./config.yml",
		"yaml":   true,
		"stdin":  true,
	})
	if err != nil {
		return err
	}

	unmarshal := yaml.Unmarshal
	if !(*flags["yaml"].(*bool)) {
		unmarshal = json.Unmarshal
	}

	configReader := env.New[krill.Configuration](func(cr *env.ConfigurationReader[krill.Configuration]) {
		cr.Unmarshal = unmarshal
		if *flags["stdin"].(*bool) {
			cr.ReadFile = func(_ string) ([]byte, error) {
				return io.ReadAll(os.Stdin)
			}
		}
	})

	configuration, err := configReader.Read(*flags["config"].(*string))
	if err != nil {
		return err
	}

	lg := logger.NewZeroLoggerWrapper(log.Logger, func(zlw *logger.ZeroLoggerWrapper) {
		zlw.LogLevel = configuration.LogLevel
	})

	lg.Printf("finished reading configuration")

	exp := &exporter.MockExporter{}

	lg.Printf("creating stores")

	brokerStore := broker.NewStore()
	clientStore := client.NewStore()
	edgeStore := edge.NewStore()
	formatterStore := formatter.NewStore()
	limiterStore := limiter.NewStore()
	nodeStore := node.NewStore()
	observerStore := observer.NewStore()
	outletStore := outlet.NewStore()
	providerStore := provider.NewStore()
	publisherStore := publisher.NewStore()
	registryStore := registry.NewStore()
	rendererStore := renderer.NewStore()
	siteStore := site.NewStore()
	subscriberStore := subscriber.NewStore()
	topicStore := topic.NewStore()
	tracerStore := tracer.NewStore()

	lg.Printf("creating services")

	svcTag := lg.Tag("service")

	brokerService := broker.NewService(brokerStore, registryStore)
	clientService := client.NewService(ctx, clientStore, registryStore, brokerStore, siteStore, func(s *client.Service) {
		s.Logger = svcTag.Tag("client")
	})
	edgeService := edge.NewService(edgeStore, nodeStore)
	formatterService := formatter.NewService(formatterStore)
	limiterService := limiter.NewService(ctx, limiterStore)
	nodeService := node.NewService(nodeStore, func(s *node.Service) {
		s.Logger = svcTag.Tag("node")
	})
	observerService := observer.NewService(observerStore, registryStore, providerStore)
	outletService := outlet.NewService(outletStore, formatterStore, registryStore)
	providerService := provider.NewService(providerStore, reg, exp, func(s *provider.Service) {
		s.Logger = svcTag.Tag("provider")
	})
	publisherService := publisher.NewService(ctx, publisherStore, registryStore, clientStore, topicStore, rendererStore, limiterStore, tracerStore, func(s *publisher.Service) {
		s.Logger = svcTag.Tag("publisher")
	})
	registryService := registry.NewService(registryStore)
	rendererService := renderer.NewService(rendererStore, formatterStore, nodeStore)
	siteService := site.NewService(siteStore, registryStore)
	subscriberService := subscriber.NewService(subscriberStore, clientStore, topicStore, outletStore, registryStore, tracerStore, func(s *subscriber.Service) {
		s.Logger = svcTag.Tag("subscriber")
	})
	topicService := topic.NewService(topicStore, registryStore)
	tracerService := tracer.NewService(tracerStore, registryStore)

	builder := krill.New(
		brokerService,
		clientService,
		edgeService,
		formatterService,
		limiterService,
		nodeService,
		observerService,
		outletService,
		providerService,
		publisherService,
		registryService,
		rendererService,
		siteService,
		subscriberService,
		topicService,
		tracerService,
	)

	lg.Printf("parsing krill configuration")

	err = builder.Parse(configuration.Simulation)
	if err != nil {
		return err
	}

	lg.Printf("setting up metrics server")

	// Set up prometheus servers.
	promMetricsServer := &http.Server{
		ReadTimeout:       1 * time.Second,
		WriteTimeout:      1 * time.Second,
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		Addr:              fmt.Sprintf(":%d", configuration.Ports.Metrics),
	}

	promCustomMetricsServerMux := http.NewServeMux()
	promCustomMetricsServerMux.Handle(
		"/metrics",
		promhttp.HandlerFor(
			reg,
			promhttp.HandlerOpts{Registry: reg},
		),
	)

	promMetricsServer.Handler = promCustomMetricsServerMux

	go func() {
		<-ctx.Done()
		err := promMetricsServer.Close()
		if err != nil {
			panic(err)
		}
	}()

	lg.Printf("finished setup")

	return promMetricsServer.ListenAndServe()
}
