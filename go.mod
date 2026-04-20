module github.com/macabc/muzika

go 1.26.1

require (
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/golang-migrate/migrate/v4 v4.17.1
	github.com/google/uuid v1.6.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/macabc/gosk v0.0.0-00010101000000-000000000000
	golang.org/x/crypto v0.23.0
	modernc.org/sqlite v1.49.1
)

require (
	github.com/bh90210/soul v1.1.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/matoous/go-nanoid/v2 v2.1.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rs/zerolog v1.33.0 // indirect
	github.com/teivah/broadcast v0.1.0 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	modernc.org/libc v1.72.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// Run `go mod tidy` after cloning to populate indirect requirements.

replace github.com/macabc/gosk => /Users/macabc/gosk
