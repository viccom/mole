package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/eclipse/paho.golang/packets"
	kcp "github.com/xtaci/kcp-go/v5"
)

func main() {
	broker := flag.String("broker", "mqtt://127.0.0.1:1883", "Broker URL (mqtt://user:pass@host:port or kcp://user:pass@host:port)")
	topic := flag.String("topic", "test/topic", "Topic")
	mode := flag.String("mode", "sub", "Mode: pub or sub")
	msg := flag.String("message", "hello from mqtt-kcp-test", "Message to publish")
	qos := flag.Int("qos", 0, "QoS (0/1/2)")
	flag.Parse()

	if *mode != "pub" && *mode != "sub" {
		log.Fatalf("invalid mode %q, use pub or sub", *mode)
	}

	u, err := url.Parse(*broker)
	if err != nil {
		log.Fatalf("parse broker URL: %v", err)
	}
	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	conn, err := dial(ctx, u)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	log.Printf("connected to %s (%s)", u.Host, u.Scheme)

	client := paho.NewClient(paho.ClientConfig{
		Conn: packets.NewThreadSafeConn(conn),
		OnClientError: func(err error) {
			log.Printf("client error: %v", err)
		},
	})

	_, err = client.Connect(ctx, &paho.Connect{
		ClientID:     fmt.Sprintf("mqtt-kcp-test-%d", os.Getpid()),
		KeepAlive:    30,
		CleanStart:   true,
		Username:     username,
		UsernameFlag: username != "",
		Password:     []byte(password),
		PasswordFlag: password != "",
	})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer client.Disconnect(&paho.Disconnect{ReasonCode: 0})
	log.Println("MQTT CONNECT OK")

	switch *mode {
	case "pub":
		publish(ctx, client, *topic, *msg, byte(*qos))
	case "sub":
		subscribe(ctx, cancel, client, *topic, byte(*qos))
	}
}

func dial(ctx context.Context, u *url.URL) (net.Conn, error) {
	if u.Scheme == "kcp" {
		return dialKCP(ctx, u.Host)
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return (&net.Dialer{}).DialContext(dialCtx, "tcp", u.Host)
}

func dialKCP(ctx context.Context, addr string) (net.Conn, error) {
	type result struct {
		conn *kcp.UDPSession
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		sess, err := kcp.DialWithOptions(addr, nil, 0, 0)
		ch <- result{sess, err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		r.conn.SetNoDelay(1, 10, 2, 1)
		return r.conn, nil
	}
}

func publish(ctx context.Context, client *paho.Client, topic, message string, qos byte) {
	_, err := client.Publish(ctx, &paho.Publish{
		Topic:   topic,
		QoS:     qos,
		Payload: []byte(message),
	})
	if err != nil {
		log.Fatalf("publish: %v", err)
	}
	log.Printf("PUB topic=%s qos=%d payload=%q", topic, qos, message)
}

func subscribe(ctx context.Context, _ context.CancelFunc, client *paho.Client, topic string, qos byte) {
	client.AddOnPublishReceived(func(pr paho.PublishReceived) (bool, error) {
		log.Printf("RECV topic=%s payload=%q", pr.Packet.Topic, string(pr.Packet.Payload))
		return true, nil
	})

	_, err := client.Subscribe(ctx, &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: qos}},
	})
	if err != nil {
		log.Fatalf("subscribe: %v", err)
	}
	log.Printf("SUB topic=%s qos=%d — waiting for messages (Ctrl+C to quit)", topic, qos)

	<-ctx.Done()
}
