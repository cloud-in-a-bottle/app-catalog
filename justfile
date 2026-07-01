bin := "./bin/openhost-catalog"

test:
    go test ./...

compile:
    mkdir -p ./bin
    go build -o {{bin}} ./cmd/openhost-catalog/

run: compile
    CATALOG_DB_PATH=./bin/catalog-local.db \
    LISTEN_ADDR=:9878 \
    {{bin}}
