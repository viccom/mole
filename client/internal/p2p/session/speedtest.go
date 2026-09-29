//go:build p2p

package session

import (
	"errors"
	"io"
	"log"
	"net"
	"time"

	"moleAgent_client/internal/p2p/netutil"
)

// SpeedtestResult holds the outcome of a speedtest run.
type SpeedtestResult struct {
	SizeMB  int
	Elapsed time.Duration
	Mbps    float64
}

// RunSpeedtest runs a speedtest over a new mux stream and returns the result.
// Blocks until complete.
func RunSpeedtest(mux StreamMux, totalMB int) SpeedtestResult {
	const chunkSize = 524288 // 512 KB
	totalBytes := totalMB * 1024 * 1024

	st, err := mux.OpenStream()
	if err != nil {
		log.Printf("[SPEEDTEST] open stream: %v", err)
		return SpeedtestResult{SizeMB: totalMB}
	}
	defer st.Close()

	payload := make([]byte, chunkSize)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	start := time.Now()
	sent := 0
	for sent < totalBytes {
		n := chunkSize
		if sent+n > totalBytes {
			n = totalBytes - sent
		}
		if _, e := st.Write(payload[:n]); e != nil {
			log.Printf("[SPEEDTEST] write: %v", e)
			return SpeedtestResult{SizeMB: totalMB, Elapsed: time.Since(start)}
		}
		sent += n
	}
	st.Close()
	elapsed := time.Since(start)
	mbps := float64(sent) * 8 / elapsed.Seconds() / 1_000_000
	log.Printf("[SPEEDTEST] sent %d MB in %v = %.2f Mbps",
		sent/(1024*1024), elapsed.Round(time.Millisecond), mbps)
	return SpeedtestResult{SizeMB: totalMB, Elapsed: elapsed, Mbps: mbps}
}

// HandleSpeedtestRX receives speedtest stream data and computes throughput.
//
// initial is the already-read 4-byte header (non-FileMagic → speedtest).
// Logs progress every 2s; prints final rate when stream closes.
// onComplete is invoked with the final result when the stream closes
// (may be nil for no callback).
func HandleSpeedtestRX(st net.Conn, initial []byte, onComplete func(SpeedtestResult)) {
	start := time.Now()
	received := int64(len(initial))
	buf := make([]byte, 65535)
	lastLog := time.Now()
	for {
		n, err := st.Read(buf)
		received += int64(n)
		if time.Since(lastLog) > 2*time.Second {
			log.Printf("[SPEEDTEST] rx %s (%.1f MB/s)",
				netutil.FormatSize(received),
				float64(received)/time.Since(start).Seconds()/(1<<20))
			lastLog = time.Now()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("[SPEEDTEST] error: %v", err)
			}
			break
		}
	}
	if received <= int64(len(initial)) {
		return
	}
	elapsed := time.Since(start)
	mbps := float64(received) * 8 / elapsed.Seconds() / 1_000_000
	log.Printf("[SPEEDTEST] rx %s in %v = %.2f Mbps",
		netutil.FormatSize(received), elapsed.Round(time.Millisecond), mbps)
	if onComplete != nil {
		onComplete(SpeedtestResult{
			SizeMB:  int(received) / (1024 * 1024),
			Elapsed: elapsed,
			Mbps:    mbps,
		})
	}
}
