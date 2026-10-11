// Package pprofserver serves the runtime's profiles on the shared HTTP
// listener: the standard net/http/pprof endpoints under /debug/pprof, plus a
// visualizer that renders one profile as a flame graph and a top table. The
// routes are disabled unless the http.pprof block says otherwise, and it
// authenticates callers when http.pprof.auth is configured, which is required
// on any listener reachable beyond loopback.
package pprofserver
