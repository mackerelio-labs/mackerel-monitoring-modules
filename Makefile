.PHONY: build
build:
	$(MAKE) -C cloudwatch-logs-aggregator build

.PHONY: test
test:
	$(MAKE) -C cloudwatch-logs-aggregator test

.PHONY: bump-version
bump-version:
	$(MAKE) -C cloudwatch-logs-aggregator bump-version
