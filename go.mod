module github.com/tabulum/tabulum

go 1.25.0

// Dependencies will be added by Claude Code during implementation.
// Expected dependencies:
//   go.etcd.io/bbolt          — embedded key-value store
//   golang.org/x/time         — rate limiting (token bucket)
//   golang.org/x/crypto       — bcrypt for API key/token hashing
//   gopkg.in/yaml.v3          — config file parsing
//   github.com/google/uuid    — UUID generation

require (
	github.com/google/uuid v1.6.0
	go.etcd.io/bbolt v1.4.3
	golang.org/x/crypto v0.49.0
	golang.org/x/time v0.15.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/sys v0.42.0 // indirect
)
