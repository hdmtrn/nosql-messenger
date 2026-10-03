package main

import (
	"net/http"
	"net/http/pprof"
	"time"
)

// internalAddr serves what must never face the internet: profiles now, metrics
// later. Loopback keeps it inside the container, or inside the ECS task, where
// a sidecar shares the network namespace and still reaches it; compose
// publishes nothing for it.
const internalAddr = "127.0.0.1:9090"

// internalRoutes is a mux of its own. Importing net/http/pprof also registers
// the handlers on http.DefaultServeMux, which nothing here serves; routes()
// builds a separate mux, so the public listener never carries them.
func internalRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

func internalServer() *http.Server {
	return &http.Server{
		Addr:              internalAddr,
		Handler:           internalRoutes(),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: a CPU profile or a trace streams for as many seconds
		// as asked, and pprof refuses a duration longer than the timeout.
	}
}
