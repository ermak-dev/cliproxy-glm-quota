PLUGIN := glm-quota
TARGET := linux/amd64

.PHONY: build test vet fmt clean

build:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		go build -buildmode=c-shared -o dist/linux/amd64/$(PLUGIN).so .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf dist
