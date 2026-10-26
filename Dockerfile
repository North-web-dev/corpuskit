FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/corpuskit ./cmd/corpuskit

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/corpuskit /usr/local/bin/corpuskit
USER nonroot:nonroot
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/corpuskit"]
CMD ["--help"]
