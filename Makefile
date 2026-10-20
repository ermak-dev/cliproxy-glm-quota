PLUGIN := glm-quota

.PHONY: build test vet fmt check clean

build:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		go build -buildmode=c-shared -o dist/linux/amd64/$(PLUGIN).so .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || \
		{ echo "gofmt above; run: gofmt -w ." >&2; exit 1; }

check: fmt vet test

clean:
	rm -rf dist
