package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"
)

var (
	rootLogger = logrus.New()
	appLogger  = rootLogger.WithField("app", "demoware")
)

func main() {
	rootLogger.SetOutput(os.Stderr)
	app := &cli.App{
		Name:  "demoware",
		Usage: "A minimal test server that simulates a poll-able metrics stream",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "listen-address", Value: ":8080", Usage: "the address to listen for incoming API connections"},
			&cli.StringFlag{Name: "listen-tls-key", Value: "", Usage: "path to a file with a TLS cert for the server (enables TLS support)"},
			&cli.StringFlag{Name: "listen-tls-password", Value: "", Usage: "path to the TLS key for the server (enables TLS support)"},
			// Options for metrics generation
			&cli.StringFlag{Name: "metrics-endpoint", Value: "/metrics", Usage: "endpoint for serving metrics requests"},
			&cli.UintFlag{Name: "metrics-min-count", Value: 0, Usage: "minimum number of metrics to return in responses"},
			&cli.UintFlag{Name: "metrics-max-count", Value: 10, Usage: "maximum number of metrics to return in responses"},
		},
		Action: demowareApp,
	}

	if err := app.Run(os.Args); err != nil {
		exitWithError(err)
	}
}

// demowareApp is the entrypoint for the test server.
func demowareApp(cliCtx *cli.Context) error {
	ctx := signalAwareContext(context.Background())
	mux := http.NewServeMux()
	registerMetricsHandler(cliCtx, mux)

	srv, err := startServer(cliCtx, mux)
	if err != nil {
		return err
	}

	// Wait for signals and shutdown
	<-ctx.Done()
	appLogger.Info("shutting down server")
	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFn()
	_ = srv.Shutdown(timeoutCtx)
	return nil
}

// startServer creates a new http.Server instance using the configuration
// settings from the provided CLI context and spins up a goroutine to handle
// incoming connections.
func startServer(cliCtx *cli.Context, mux http.Handler) (*http.Server, error) {
	var (
		listenAt    = cliCtx.String("listen-address")
		tlsCertFile = cliCtx.String("listen-tls-key")
		tlsKeyFile  = cliCtx.String("listen-tlk-password")
		srv         = &http.Server{Handler: mux}
	)

	l, err := net.Listen("tcp", listenAt)
	if err != nil {
		return nil, xerrors.Errorf("unable to create listener: %w", err)
	}

	if tlsCertFile != "" && tlsKeyFile != "" {
		cert, certErr := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
		if certErr != nil {
			return nil, xerrors.Errorf("unable to load TLS certificate: %w", certErr)
		}

		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	}

	go doServe(srv, l)
	return srv, err
}

// doServe starts serving incoming API requests.
func doServe(srv *http.Server, l net.Listener) {
	useTLS := srv.TLSConfig != nil
	appLogger.WithFields(logrus.Fields{
		"use_tls":   srv.TLSConfig != nil,
		"listen_at": l.Addr().String(),
	}).Info("listening for incoming connections")

	if useTLS {
		_ = srv.ServeTLS(l, "", "")
	} else {
		_ = srv.Serve(l)
	}
}

// signalAwareContext returns a context.Context that gets cancelled when the
// process receives a HUP or INT signal.
func signalAwareContext(ctx context.Context) context.Context {
	wrappedCtx, cancelFn := context.WithCancel(ctx)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT)
		s := <-sigCh
		appLogger.WithField("signal", s.String()).Info("terminating due to signal")
		cancelFn()
	}()
	return wrappedCtx
}

// exitWithError logs a captured error and exits the application with a
// non-zero status code.
func exitWithError(err error) {
	appLogger.WithError(err).Errorf("terminating due to error")
	os.Exit(1)
}

type metricsEnvelope struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type loadAvgMetric struct {
	Value float32 `json:"value"`
}

type cpuUsageMetric struct {
	Value []float32 `json:"value"`
}

type lastKernelUpgrade struct {
	Value time.Time `json:"value"`
}

// registerMetricsHandler generates a handler for the metrics endpoint that is
// parametrized by the contents of the provided CLI context and registers it
// to the provided ServeMux.
func registerMetricsHandler(cliCtx *cli.Context, mux *http.ServeMux) {
	endpoint := cliCtx.String("metrics-endpoint")
	minMetrics := int32(cliCtx.Uint("metrics-min-count"))
	maxMetrics := int32(cliCtx.Uint("metrics-max-count"))
	if minMetrics > maxMetrics {
		exitWithError(xerrors.Errorf("invalid metrics count params: min-count > max-count"))
	}

	appLogger.WithField("endpoint", endpoint).Info("registered metrics handler")

	mux.Handle(endpoint, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		numMetrics := rand.Int31n(maxMetrics-minMetrics) + minMetrics
		metricsList := make([]metricsEnvelope, numMetrics)
		for i := int32(0); i < numMetrics; i++ {
			switch rand.Int31n(3) {
			case 0:
				metricsList[i].Type = "load_avg"
				metricsList[i].Payload = loadAvgMetric{
					Value: rand.Float32(),
				}
			case 1:
				metricsList[i].Type = "cpu_usage"
				values := make([]float32, 5)
				for i := 0; i < len(values); i++ {
					values[i] = rand.Float32()
				}
				metricsList[i].Payload = cpuUsageMetric{
					Value: values,
				}
			case 2:
				metricsList[i].Type = "last_kernel_upgrade"
				metricsList[i].Payload = lastKernelUpgrade{
					Value: time.Now(),
				}
			}
		}

		// Serialize response
		if err := json.NewEncoder(w).Encode(metricsList); err != nil {
			appLogger.WithError(err).Error("GET /metrics")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		appLogger.WithField("num_metrics", numMetrics).Info("GET /metrics")
	}))
}
