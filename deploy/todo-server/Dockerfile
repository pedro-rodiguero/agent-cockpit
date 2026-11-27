# cockpit-todo-server in a small image. Build from the repository root:
#   docker build -f deploy/todo-server/Dockerfile -t cockpit-todo-server .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/cockpit-todo-server ./cmd/cockpit-todo-server \
	&& mkdir /data

# No shell and no package manager, and the server runs as a user without privileges.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cockpit-todo-server /usr/local/bin/cockpit-todo-server
COPY --from=build --chown=nonroot:nonroot /data /data
ENV TODO_DATA=/data
VOLUME /data
USER nonroot
ENTRYPOINT ["/usr/local/bin/cockpit-todo-server"]
CMD ["serve"]
