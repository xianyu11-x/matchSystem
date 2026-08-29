module matchsystem/apps/web

go 1.23

// This intentionally empty nested module keeps the JavaScript dependency tree
// out of the repository-root `go test ./...` package walk. The web app is not
// a Go package and has no Go runtime dependency.
