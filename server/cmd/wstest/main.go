// Command wstest is a dev tool that drives a Web Shell session over WebSocket
// and prints assertions (used by the M2 e2e check; not part of the server).
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type frame struct {
	T      string `json:"t"`
	Data   string `json:"data,omitempty"`
	Rule   string `json:"rule,omitempty"`
	Reason string `json:"reason,omitempty"`
	Token  string `json:"token,omitempty"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: wstest <wsURL> <token>")
		os.Exit(2)
	}
	url, token := os.Args[1], os.Args[2]
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		fmt.Println("DIAL_ERR:", err)
		os.Exit(1)
	}
	defer func() { _ = c.CloseNow() }()

	var mu sync.Mutex
	var out strings.Builder
	gotBlock, gotConfirm := false, false
	go func() {
		for {
			var f frame
			if err := wsjson.Read(ctx, c, &f); err != nil {
				return
			}
			mu.Lock()
			switch f.T {
			case "stdout":
				if b, e := base64.StdEncoding.DecodeString(f.Data); e == nil {
					out.Write(b)
				}
			case "filter_block":
				gotBlock = true
				fmt.Println("FILTER_BLOCK rule=" + f.Rule)
			case "filter_confirm":
				gotConfirm = true
				fmt.Println("FILTER_CONFIRM rule=" + f.Rule)
			case "closed":
				fmt.Println("CLOSED reason=" + f.Reason)
			}
			mu.Unlock()
		}
	}()

	send := func(s string) {
		_ = wsjson.Write(ctx, c, frame{T: "stdin", Data: base64.StdEncoding.EncodeToString([]byte(s))})
	}
	time.Sleep(1200 * time.Millisecond)
	send("whoami\n")
	time.Sleep(900 * time.Millisecond)
	send("rm -rf /\n") // expect block
	time.Sleep(900 * time.Millisecond)
	send("shutdown -h now\n") // expect confirm (we don't confirm)
	time.Sleep(900 * time.Millisecond)

	mu.Lock()
	o := out.String()
	fmt.Printf("OUTPUT_LEN=%d\n", len(o))
	fmt.Printf("OUTPUT_HAS_WHOAMI=%v\n", strings.Contains(o, "whoami"))
	fmt.Printf("OUTPUT_HAS_USER=%v\n", strings.Contains(o, "ops-yusui"))
	fmt.Printf("GOT_BLOCK=%v\n", gotBlock)
	fmt.Printf("GOT_CONFIRM=%v\n", gotConfirm)
	mu.Unlock()
}
