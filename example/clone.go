package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/tidwall/redcon"
)

var (
	addr  = ":6380"
	proto = "tcp"
)

func init() {
	flag.StringVar(&proto, "proto", proto, "set protocl(tcp or quic)")
}

func main() {
	flag.Parse()
	var mu sync.RWMutex
	var items = make(map[string][]byte)
	var ps redcon.PubSub
	go log.Printf("started server at %s", addr)
	if !strings.EqualFold(proto, "tcp") && !strings.EqualFold(proto, "quic") {
		log.Fatalf("invalid proto %s", proto)
	}
	err := redcon.ListenAndServeNetwork(proto, addr,
		func(conn redcon.Conn, cmd redcon.Command) {
			switch strings.ToLower(string(cmd.Args[0])) {
			default:
				conn.WriteError("ERR unknown command '" + string(cmd.Args[0]) + "'")
			case "publish":
				// Publish to all pub/sub subscribers and return the number of
				// messages that were sent.
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}
				count := ps.Publish(string(cmd.Args[1]), string(cmd.Args[2]))
				conn.WriteInt(count)
			case "subscribe", "psubscribe":
				// Subscribe to a pub/sub channel. The `Psubscribe` and
				// `Subscribe` operations will detach the connection from the
				// event handler and manage all network I/O for this connection
				// in the background.
				if len(cmd.Args) < 2 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}
				command := strings.ToLower(string(cmd.Args[0]))
				for i := 1; i < len(cmd.Args); i++ {
					if command == "psubscribe" {
						ps.Psubscribe(conn, string(cmd.Args[i]))
					} else {
						ps.Subscribe(conn, string(cmd.Args[i]))
					}
				}
			case "test":
				test(conn)
			case "detach":
				hconn := conn.Detach()
				log.Printf("connection has been detached")
				go func() {
					defer hconn.Close()
					hconn.WriteString("OK")
					hconn.Flush()
				}()
			case "ping":
				conn.WriteString("PONG")
			case "quit":
				conn.WriteString("OK")
				conn.Close()
			case "set":
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}
				mu.Lock()
				items[string(cmd.Args[1])] = cmd.Args[2]
				mu.Unlock()
				conn.WriteString("OK")
			case "get":
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}
				mu.RLock()
				val, ok := items[string(cmd.Args[1])]
				mu.RUnlock()
				if !ok {
					conn.WriteNull()
				} else {
					conn.WriteBulk(val)
				}
			case "del":
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}
				mu.Lock()
				_, ok := items[string(cmd.Args[1])]
				delete(items, string(cmd.Args[1]))
				mu.Unlock()
				if !ok {
					conn.WriteInt(0)
				} else {
					conn.WriteInt(1)
				}
			case "config":
				// This simple (blank) response is only here to allow for the
				// redis-benchmark command to work with this example.
				conn.WriteArray(2)
				conn.WriteBulk(cmd.Args[2])
				conn.WriteBulkString("")
			}
		},
		func(conn redcon.Conn) bool {
			// Use this function to accept or deny the connection.
			log.Printf("accept: %s", conn.RemoteAddr())
			if strings.EqualFold(proto, "quic") {
				test(conn)
			}
			return true
		},
		func(conn redcon.Conn, err error) {
			// This is called when the connection has been closed
			// log.Printf("closed: %s, err: %v", conn.RemoteAddr(), err)
			log.Printf("closed")
		},
	)
	if err != nil {
		log.Fatal(err)
	}
}

func test(conn redcon.Conn) {
	conn.Start()
	dc := conn.Detach()
	go func(dc redcon.DetachedConn) {
		//dc.WriteString("OK")
		//dc.Flush()
		for {
			cmd, err := dc.ReadMyCommand()
			if err != nil {
				panic(err)
			}
			fmt.Println(cmd.Args)
			dc.WriteArray(len(cmd.Args))
			for i := range cmd.Args {
				dc.WriteBulk(cmd.Args[i])
			}
			dc.Flush()
			dc.Reclaim(&cmd)
		}
	}(dc)

}
