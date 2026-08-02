# openai-server

A small, in-memory **reference server** for the OpenAI-compatible **Files + Batch** API, in Go. It
speaks the exact wire shapes (via [`github.com/getotium/openai`](https://github.com/getotium/openai))
so a **base-URL-swapped OpenAI client works against it**, and runs each batch request through a
**pluggable `Runner`**.

It's a conformance harness and a starting point — everything is in memory, there's no auth, and the
built-in runner just echoes. Swap the `Runner` (and the storage, if you fork it) to point it at a
real backend. **What's open is the harness and the contract; what you plug in is yours.**

## Run it

```
go run ./cmd/openai-server -addr :8080
```

Then point any OpenAI client at `http://localhost:8080/v1` (no API key needed) and drive the batch
flow: upload a `.jsonl`, create a batch, poll it, download the output.

## Endpoints

`POST /v1/files` · `GET /v1/files/{id}` · `GET /v1/files/{id}/content` · `POST /v1/batches` ·
`GET /v1/batches` · `GET /v1/batches/{id}` · `POST /v1/batches/{id}/cancel`

## The `Runner` seam

```go
type Runner func(ctx context.Context, req openai.ParsedRequest) (openai.BatchRequestOutput, error)

srv := server.New(server.EchoRunner) // ← swap in your own to hit vLLM / an upstream API / a local model
http.ListenAndServe(":8080", srv.Handler())
```

A `Runner` shapes one batch line into its output. Per-request failures go in the output's `Error`
field (they don't fail the batch); a returned error is a batch-level fault. `EchoRunner` is the
zero-dependency reference — the whole server (and its tests) work out of the box.

## Conformance

Two suites prove the surface is real, not "compatible-ish":

- **`conformance_test.go`** drives the server with the **official [`openai-go`](https://github.com/openai/openai-go)
  SDK** — upload → create → poll → download — so "a stock OpenAI client works by base-URL swap" is a
  passing test, not a claim.
- **`server_test.go`** exercises the same batch lifecycle over raw HTTP.

Both run in CI on every change.

## Provenance

The OpenAI-compatible surface [Otium](https://getotium.ai) exposes so customers integrate by
pointing an existing OpenAI client at it, published as an independent reference. The scheduling,
pricing, and model-selection that sit behind Otium's own server are not here — this is the open,
conformant harness.

## License

[Apache-2.0](LICENSE).
