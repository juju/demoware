package main

import (
	"context"
	"crypto/tls"
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
