.PHONY: build
build:
	GOOS=linux GOARCH=amd64 go build -o bootstrap
	zip function.zip bootstrap
	rm bootstrap

.PHONY: test
test:
	go test

.PHONY: bump-version
bump-version:
	perl -i -pe 's/version = "[\d\.]+"/version = "$(VERSION)"/g' version.go
