FROM golang:1.26.9-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
COPY services/banking-service/go.mod services/banking-service/go.sum ./services/banking-service/
WORKDIR /src/services/banking-service
RUN go mod download
COPY backend /src/backend
COPY services/banking-service /src/services/banking-service
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/banking-service ./cmd/server
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/banking-migrate ./cmd/migrate
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/banking-seed ./cmd/seed
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/banking-storage-migrate ./cmd/storage-migrate
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/banking-storage-control ./cmd/storage-control

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/banking-service /banking-service
COPY --from=build /out/banking-migrate /banking-migrate
COPY --from=build /out/banking-seed /banking-seed
COPY --from=build /out/banking-storage-migrate /banking-storage-migrate
COPY --from=build /out/banking-storage-control /banking-storage-control
EXPOSE 8090
ENTRYPOINT ["/banking-service"]
