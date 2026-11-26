# Ticket rush: rate limiting vs waiting room vs load shedding

A small Go simulation of what happens when a ticket sale opens and **20,000 buyers arrive within the same
10 seconds** at a checkout that can handle **500 requests per second**. It compares four setups:

| Scenario | What sits in front of the checkout |
|---|---|
| **No protection** | Nothing. Every request goes into the backend queue. |
| **Rate limiting** | A global and a per-user **token bucket** ([`golang.org/x/time/rate`](https://pkg.go.dev/golang.org/x/time/rate)). Over the limit → `429 Too Many Requests` with `Retry-After`. |
| **Waiting room** | Buyers wait in a queue and are let into the checkout at a controlled rate, with a cap on active users. The admission rate is a token bucket. |
| **Load shedding** | When the backend queue is too long, it answers `503` right away, dropping catalog traffic first and checkout last. |

It is the companion repo of the blog post *"Salen las entradas a las 20:00"* ([Spanish](https://patricioponcini.com.ar/blog/rate-limiting-sala-de-espera) · [English](https://patricioponcini.com.ar/en/blog/rate-limiting-sala-de-espera)).

## Running it

You need **Go 1.26+**. No Docker and no other dependencies.

```bash
git clone https://github.com/PatricioPoncini/ticket-rush.git
cd ticket-rush

go run ./cmd/server     # interactive demo on http://localhost:8080
go run ./cmd/sim        # headless: prints a summary and writes out/results.csv + SVG charts
go test ./...           # tests
```

Or with `make`: `make run`, `make sim`, `make test`.

In the demo you choose the scenarios and parameters (buyers, capacity, waiting room admission rate, FIFO or
random order, share of impatient clients, seed) and press **Open the sale**. The chart shows successful
purchases per second. Each panel follows one buyer (#10,001, right in the middle of the crowd): how many
people are ahead of them in the waiting room, how many requests and errors they went through, and whether
they bought or gave up. Opening `http://localhost:8080/#run` starts the sale right away.

## What you should see

With the default parameters (`go run ./cmd/sim`):

| scenario | bought | gave up | wait p50 | wait p95 | requests/buyer | errors/buyer | catalog ok | wasted work | fairness |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| No protection | 1,872 | 18,128 | 1.5 s | 2.9 s | 19.4 | 18.9 | 0% | 118,010 | 1.00 |
| Rate limiting | 20,000 | 0 | 16.1 s | 37.1 s | 22.5 | 21.5 | 91% | 0 | 0.12 |
| Waiting room | 20,000 | 0 | 23.6 s | 44.9 s | 1.0 | 0.0 | 100% | 0 | 1.00 |
| Load shedding | 20,000 | 0 | 14.5 s | 33.3 s | 11.6 | 10.6 | 84% | 0 | 0.24 |

- **Wasted work**: requests the backend finished after the client had already stopped waiting.
- **Fairness**: Spearman rank correlation between arrival order and purchase time, among buyers who bought.
  1 means strict first come, first served.

Things worth noticing:

- **Without protection, goodput collapses.** The backend stays 100% busy, but once the queue is longer than the
  client timeout it is answering requests nobody is waiting for. Clients retry, the queue grows further, and
  almost nobody buys. The first ~1,900 buyers get in before the queue builds up.
- **Rate limiting saves the backend, not the experience.** Everyone eventually buys, but the average buyer
  sees about 20 errors. Who gets in first is close to random (fairness 0.12): it depends on whose retry lands
  when a token is free.
- **The waiting room is slower on paper and much better in practice.** It admits 350 buyers per second, below
  capacity on purpose to leave room for the rest of the site. In exchange each buyer sends **one** request,
  sees no errors, and is served in arrival order.
- **Load shedding protects what matters.** The backend never goes past the client timeout, and the catalog is
  sacrificed before the checkout. Buyers still see errors, so it is a last line of defense, not a user experience.

## The model

This is a **discrete-event simulation in virtual time**, deterministic for a given seed. It is meant to show
mechanisms, not to predict the numbers of any real system. All parameters are in
[`internal/sim/config.go`](internal/sim/config.go).

- **Buyers** arrive uniformly within the first 10 s. Each wants one checkout and gives up after a random
  patience of 60 to 180 s.
- **Catalog traffic** (browsing, search) arrives as a Poisson process at 100 req/s, with no retries.
- **The backend** has 10 workers at 20 ms per request (500 req/s) and an unbounded FIFO queue. It does not
  know whether the client is still waiting, so it keeps processing stale requests. That is the source of the
  wasted work.
- **Clients** time out after 3 s. On failure they retry with exponential backoff and full jitter, or after
  `Retry-After` when the server sends one. 10% are "impatient": they ignore all of that and retry every 250 ms.
- **Rate limiting**: a per-user bucket (1 req/s, burst 2) and a global bucket (500 req/s, burst 50) for the
  whole backend, catalog included. Rejections are `429` with `Retry-After: 1`.
- **Waiting room**: admission of up to 350 new users/s (token bucket) and at most 1,000 active users. The
  queue order is FIFO or random. Waiting in the room costs the backend nothing.
- **Load shedding**: catalog is rejected when the estimated queue wait is above 100 ms, and checkout when it is
  above 1 s. Rejections are `503` and are assumed to be free.

Simplifications to keep in mind: rejecting a request costs nothing, every request costs the same, there is
no inventory (everyone can buy), and the backend does not autoscale.

## Layout

```
cmd/server/     HTTP server: embedded UI + /api/run (Server-Sent Events, one frame per simulated second)
cmd/sim/        headless runner: summary table, out/results.csv, out/goodput-*.svg, out/fairness-*.svg
internal/sim/   the simulation: config, engine, backend, metrics
internal/chart/ SVG charts (standard library only)
web/            UI: index.html, app.js, style.css
```

## References

- RFC 6585 §4, [429 Too Many Requests](https://www.rfc-editor.org/rfc/rfc6585#section-4) · RFC 9110 §10.2.3, [Retry-After](https://www.rfc-editor.org/rfc/rfc9110#section-10.2.3)
- Stripe, [Scaling your API with rate limiters](https://stripe.com/blog/rate-limiters)
- AWS, [Throttle requests to your REST APIs](https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-request-throttling.html) (API Gateway token bucket)
- Go, [`golang.org/x/time/rate`](https://pkg.go.dev/golang.org/x/time/rate)
- AWS Builders' Library, [Timeouts, retries, and backoff with jitter](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
- AWS Builders' Library, [Using load shedding to avoid overload](https://aws.amazon.com/builders-library/using-load-shedding-to-avoid-overload/)
- Google SRE Book, [Handling Overload](https://sre.google/sre-book/handling-overload/) and [Addressing Cascading Failures](https://sre.google/sre-book/addressing-cascading-failures/)
- Cloudflare, [Waiting Room docs](https://developers.cloudflare.com/waiting-room/about/), [queueing methods](https://developers.cloudflare.com/waiting-room/reference/queueing-methods/) and [Building Waiting Room on Workers and Durable Objects](https://blog.cloudflare.com/building-waiting-room-on-workers-and-durable-objects/)

## License

MIT
