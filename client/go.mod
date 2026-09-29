module moleAgent_client

go 1.25.8

require (
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/gorilla/websocket v1.5.3
	github.com/hashicorp/yamux v0.1.2
	github.com/pion/dtls/v3 v3.1.5
	github.com/pion/stun/v3 v3.1.6
	github.com/pkg/errors v0.9.1
	github.com/pkg/sftp v1.13.10
	github.com/shirou/gopsutil/v3 v3.24.5
	github.com/viccom/go-selfupdater v0.0.0-20260519015723-18c3e4632e06
	github.com/xtaci/kcp-go/v5 v5.6.72
	github.com/xtaci/smux v1.5.57
	go.bug.st/serial v1.6.4
	golang.org/x/crypto v0.52.0
	golang.org/x/net v0.54.0
	golang.org/x/sys v0.45.0
	mole/shared v0.0.0
)

replace mole/shared => ../shared

require (
	github.com/creack/goselect v0.1.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.6 // indirect
	github.com/klauspost/reedsolomon v1.12.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v4 v4.0.2 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/time v0.14.0 // indirect
)
