.PHONY: test test-unit build vet fmt run seed reseed flows tidy

# Default: everything that runs on the Go toolchain alone — no Docker, no
# network, no database. Covers the router's auth gates on every registered
# route, config validation, the slot arithmetic, the report arithmetic and
# the geofence maths. Run this constantly.
test: vet test-unit

test-unit:
	go test ./internal/... ./pkg/... -count=1

build:
	go build ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal pkg

tidy:
	go mod tidy

# Needs a MongoDB at MONGO_URI.
run:
	go run ./cmd/api

# Fills an empty database with a day's worth of demo data. Refuses to touch
# a database that already has users; `reseed` is the explicit wipe.
seed:
	go run ./cmd/seed

reseed:
	go run ./cmd/seed -reset

# Drives every flow against a running server and a seeded database, and
# prints a pass/fail line per rule. Needs both, so it is not part of `test`.
flows:
	python scripts/flows.py
