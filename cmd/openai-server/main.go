// Command openai-server runs the reference OpenAI-compatible Files + Batch API server with the
// built-in echo runner — enough to point a stock OpenAI client at and watch a batch round-trip.
// Replace server.EchoRunner with your own Runner to wire a real inference backend.
package main

import (
	"flag"
	"log"
	"net/http"

	server "github.com/getotium/openai-server"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	srv := server.New(server.EchoRunner)
	log.Printf("openai-server (reference, echo runner) listening on %s", *addr)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
