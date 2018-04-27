all: test

# gopkg fits all v1.1, v1.2, ... in v1
PKG_STABLE = gopkg.in/dedis/cothority.v2
include $(shell go env GOPATH)/src/github.com/dedis/Coding/bin/Makefile.base
EXCLUDE_LINT = "should be.*UI|_test.go"

# You can use `test_playground` to run any test or part of cothority
# for more than once in Travis. Change `make test` in .travis.yml
# to `make test_playground`.
test_playground:
	cd ocs/service; \
	for a in $$( seq 100 ); do \
	  echo OCS $$a - $$( date ); \
	  go test -v -race -short -run TestService_proof > test_log || break ; \
	done; \
	cat test_log; \
	cd ../../skipchain; \
	for a in $$( seq 100 ); do \
	  echo Skipchain $$a - $$( date ); \
	  go test -v -race -short -timeout 5m > test_log || break ; \
	done; \
	cat test_log; \
	exit 1

# Other targets are:
# make create_stable

proto:
	awk -f proto.awk status/service/struct.go > external/proto/status.proto
