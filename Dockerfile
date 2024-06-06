# node build
from golang:1.22.2-bookworm as gobuilder
WORKDIR /
COPY . .
RUN go build -o bls-avs-tools cli/*.go

# final image
from debian:bookworm-slim

RUN apt-get update && \
        apt-get install --no-install-recommends -y curl sudo daemontools jq ca-certificates && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/*

WORKDIR /
COPY --from=gobuilder bls-avs-tools bls-avs-tools

ENTRYPOINT ["/bls-avs-tools"]