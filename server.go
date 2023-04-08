package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	v1 "github.com/arkami8/image-gem/api/v1"
	"github.com/arkami8/image-gem/config"

	gorillaHandlers "github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"github.com/unrolled/secure"
)

func Serve() {
	var wait time.Duration
	flag.DurationVar(&wait, "graceful-timeout", time.Minute*1, "the duration for which the server gracefully wait for existing connections to finish - e.g. 30s or 1m")
	flag.Parse()

	// Create router and register subrouters (subdomains)
	r := mux.NewRouter()

	r.HandleFunc("/img/url/{url:.*}", v1.ImageGet).Methods("GET")

	// Add middleware handlers
	recoveryHandler := gorillaHandlers.RecoveryHandler(gorillaHandlers.PrintRecoveryStack(true))(r)
	secureOptions := secure.Options{
		ContentTypeNosniff: true,
		FrameDeny:          true,
		BrowserXssFilter:   true,
		IsDevelopment:      false,
	}
	secureHandler := secure.New(secureOptions)
	gzipHandler := gorillaHandlers.CompressHandler(secureHandler.Handler(recoveryHandler))
	corsOptions := cors.Options{
		AllowedOrigins: config.CORSAllowedOrigins,
	}
	c := cors.New(corsOptions)
	corsHandler := c.Handler(gzipHandler)

	// Sets up server values
	srv := &http.Server{
		Handler:      corsHandler,
		Addr:         config.ServerPort,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Set SSL
	// srv.TLSConfig = &tls.Config{
	// 	PreferServerCipherSuites: true,
	// 	CurvePreferences: []tls.CurveID{
	// 		tls.CurveP256,
	// 		tls.X25519,
	// 	},
	// 	Certificates: []tls.Certificate{cert},
	// }

	// Run server
	go func() {
		// TODO: offer TLS
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("error: cannot listen and serve: %s", err.Error())
		}
		// if err := srv.ListenAndServeTLS("", ""); err != nil {
		// 	log.Fatalf("error: cannot listen and serve: %s", err.Error())
		// }
	}()

	// Shutdown
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	err := srv.Shutdown(ctx)
	if err != nil {
		log.Fatal(err)
		return
	}
	log.Println("shutting down")
	os.Exit(0)
}
