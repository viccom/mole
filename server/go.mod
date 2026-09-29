module moleAgent_Serv

go 1.25.8

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/gorilla/websocket v1.5.3
	github.com/mochi-mqtt/server/v2 v2.7.9
	github.com/nalgeon/redka v1.0.1
	github.com/pion/stun/v3 v3.1.6
	github.com/viccom/go-selfupdater v0.0.0-20260519015723-18c3e4632e06
	github.com/xtaci/kcp-go/v5 v5.6.72
	github.com/xtaci/smux v1.5.57
	golang.org/x/crypto v0.52.0
	golang.org/x/time v0.14.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.48.1
	mole/shared v0.0.0
)

replace mole/shared => ../shared

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.6 // indirect
	github.com/klauspost/reedsolomon v1.12.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/pion/dtls/v3 v3.1.5 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v4 v4.0.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rs/xid v1.4.0 // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/tools v0.44.0 // indirect
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	modernc.org/libc v1.70.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
