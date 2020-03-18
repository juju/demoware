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
	rand.Seed(time.Now().UnixNano())
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
			// Injectable options
			&cli.StringFlag{Name: "with-auth-token", Value: "", Usage: "if specified, require clients to provide basic auth token"},
			&cli.Float64Flag{Name: "with-random-error-prob", Value: 0, Usage: "if non-zero, inject errors based on the given probability"},
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

// registerMetricsHandler registers a handler for the metrics endpoint with
// the provided ServeMux.
func registerMetricsHandler(cliCtx *cli.Context, mux *http.ServeMux) {
	endpoint := cliCtx.String("metrics-endpoint")
	h := genMetricsHandler(cliCtx)

	// Wrap base handler with additional middleware
	if token := cliCtx.String("with-auth-token"); token != "" {
		h = injectAuthMiddleware(h, token)
		appLogger.WithField("auth_token", token).Info("enabling authentication for incoming requests")
	}
	if failProb := cliCtx.Float64("with-random-error-prob"); failProb != 0 {
		h = injectRandomErrorMiddleware(h, failProb)
		appLogger.WithField("fail_prob", failProb).Info("enabling random fail injector for incoming requests")
	}

	mux.Handle(endpoint, h)
	appLogger.WithField("endpoint", endpoint).Info("registered metrics handler")
}

// registerMetricsHandler generates a handler for the metrics endpoint that is
// parametrized by the contents of the provided CLI context.
func genMetricsHandler(cliCtx *cli.Context) http.Handler {
	minMetrics := int32(cliCtx.Uint("metrics-min-count"))
	maxMetrics := int32(cliCtx.Uint("metrics-max-count"))
	if minMetrics > maxMetrics {
		exitWithError(xerrors.Errorf("invalid metrics count params: min-count > max-count"))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			appLogger.WithError(err).Error("GET ", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		appLogger.WithField("num_metrics", numMetrics).Info("GET ", r.URL.Path)
	})
}

// injectAuthMiddleware wraps h with a middleware that performs basic auth
// checks for incoming requests by matching the client-provided username
// with the specified token.
func injectAuthMiddleware(h http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := r.BasicAuth()
		if !ok || user != token {
			w.WriteHeader(http.StatusUnauthorized)
			appLogger.WithError(xerrors.Errorf("authentication failed")).Error("GET ", r.URL.Path)
			return
		}

		h.ServeHTTP(w, r)
	})
}

// injectRandomErrorMiddleware wraps h with a middleware that injects errors
// with the specified probability.
func injectRandomErrorMiddleware(h http.Handler, prob float64) http.Handler {
	if prob < 0 || prob > 1 {
		exitWithError(xerrors.Errorf("random error probability must be in the (0, 1] range"))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rand.Float64() <= prob {
			w.WriteHeader(http.StatusInternalServerError)
			appLogger.WithError(xerrors.Errorf("injected error")).Error("GET ", r.URL.Path)
			return
		}

		h.ServeHTTP(w, r)
	})
}
