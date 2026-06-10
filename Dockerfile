FROM docker.io/library/golang:1.26.4 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/rayjob-submitter ./cmd/rayjob-submitter

# 2. 运行阶段
FROM gcr.io/distroless/static:nonroot

WORKDIR /app

COPY --from=builder /out/rayjob-submitter ./rayjob-submitter
COPY --from=builder /src/templates/verl-rayjob.yaml ./templates/verl-rayjob.yaml

USER nonroot:nonroot

ENTRYPOINT ["./rayjob-submitter"]