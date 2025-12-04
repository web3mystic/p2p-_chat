package main

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "strings"
    "time"

    libp2p "github.com/libp2p/go-libp2p"
    pubsub "github.com/libp2p/go-libp2p-pubsub"
    kaddht "github.com/libp2p/go-libp2p-kad-dht"
    peer "github.com/libp2p/go-libp2p/core/peer"
    multiaddr "github.com/multiformats/go-multiaddr"
    "github.com/gorilla/websocket"
)

const ServiceTag = "global-chain"

type Message struct {
    SenderID string `json:"sender"`
    Type     string `json:"type"` // block | tx | status
    Content  string `json:"content"`
}

var wsClients = make(map[*websocket.Conn]bool)
var wsUpgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
    ctx := context.Background()

    h, err := libp2p.New()
    if err != nil {
        log.Fatal(err)
    }

    addr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/p2p/%s", h.ID().String()))
    fmt.Println("Your node address:", h.Addrs()[0].Encapsulate(addr))

    dht, err := kaddht.New(ctx, h)
    if err != nil {
        log.Fatal(err)
    }

    if err := dht.Bootstrap(ctx); err != nil {
        log.Fatal(err)
    }

    // 🔌 Manually connect to .exe peer (change address if needed)
    peerAddrStr := "/ip4/127.0.0.1/tcp/61820/p2p/12D3KooWLdW8F1x5yA2DhYkYxMYpsDm48r39YCAYj1qENJn5UsN4"
    peerAddr, err := multiaddr.NewMultiaddr(peerAddrStr)
    if err != nil {
        log.Println("Invalid peer multiaddr:", err)
    } else {
        peerInfo, err := peer.AddrInfoFromP2pAddr(peerAddr)
        if err != nil {
            log.Println("Failed to get peer info:", err)
        } else if err := h.Connect(ctx, *peerInfo); err != nil {
            log.Println("❌ Failed to connect to .exe peer:", err)
        } else {
            fmt.Println("✅ Connected to .exe peer:", peerInfo.ID)
        }
    }

    for _, peerInfo := range kaddht.GetDefaultBootstrapPeerAddrInfos() {
        if err := h.Connect(ctx, peerInfo); err == nil {
            fmt.Println("Connected to bootstrap peer:", peerInfo.ID)
        }
    }

    ps, err := pubsub.NewGossipSub(ctx, h)
    if err != nil {
        log.Fatal(err)
    }

    blockSub, _ := ps.Subscribe("blocks")
    txSub, _ := ps.Subscribe("txs")
    statusSub, _ := ps.Subscribe("status")

    go handleMessages("blocks", blockSub)
    go handleMessages("txs", txSub)
    go handleMessages("status", statusSub)

    go func() {
        for {
            status := Message{
                SenderID: h.ID().String(),
                Type:     "status",
                Content:  fmt.Sprintf("Node alive @ %s", time.Now().Format(time.RFC822)),
            }
            msgBytes, _ := json.Marshal(status)
            ps.Publish("status", msgBytes)
            time.Sleep(15 * time.Second)
        }
    }()

    go startWebServer()

    scanner := bufio.NewScanner(os.Stdin)
    for {
        fmt.Print("Enter (block|tx) message: ")
        if !scanner.Scan() {
            break
        }
        input := scanner.Text()
        split := strings.SplitN(input, " ", 2)
        if len(split) < 2 {
            fmt.Println("Invalid format. Use: block {json or text}")
            continue
        }
        msg := Message{
            SenderID: h.ID().String(),
            Type:     split[0],
            Content:  split[1],
        }
        if len(msg.Content) <= 3 {
            fmt.Println("Invalid message, skipped.")
            continue
        }
        data, _ := json.Marshal(msg)
        switch split[0] {
        case "block":
            ps.Publish("blocks", data)
        case "tx":
            ps.Publish("txs", data)
        default:
            fmt.Println("Unknown type. Use 'block' or 'tx'.")
        }
    }
}

func handleMessages(topic string, sub *pubsub.Subscription) {
    ctx := context.Background()
    for {
        msg, err := sub.Next(ctx)
        if err != nil {
            continue
        }
        var m Message
        if err := json.Unmarshal(msg.Data, &m); err != nil {
            continue
        }
        if len(m.Content) <= 3 {
            continue
        }
        out := fmt.Sprintf("[%s] %s: %s", strings.ToUpper(topic), m.SenderID[:10], m.Content)
        fmt.Println(out)
        broadcastToWebClients(out)
    }
}

func startWebServer() {
    http.HandleFunc("/ws", handleWebSocket)
    http.HandleFunc("/", serveHome)
    fmt.Println("Web dashboard available at: http://localhost:8081")
    log.Fatal(http.ListenAndServe(":8081", nil))
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
    conn, err := wsUpgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("WebSocket upgrade error:", err)
        return
    }
    defer conn.Close()
    wsClients[conn] = true

    for {
        _, _, err := conn.ReadMessage()
        if err != nil {
            delete(wsClients, conn)
            break
        }
    }
}

func broadcastToWebClients(message string) {
    for conn := range wsClients {
        err := conn.WriteMessage(websocket.TextMessage, []byte(message))
        if err != nil {
            conn.Close()
            delete(wsClients, conn)
        }
    }
}

func serveHome(w http.ResponseWriter, r *http.Request) {
    html := `
    <!DOCTYPE html>
    <html>
    <head><title>P2P Node Dashboard</title></head>
    <body>
        <h2>📡 Global P2P Node Messages</h2>
        <ul id="msgs"></ul>
        <script>
            let ws = new WebSocket("ws://" + location.host + "/ws");
            ws.onmessage = (e) => {
                let li = document.createElement("li");
                li.innerText = e.data;
                document.getElementById("msgs").appendChild(li);
            };
        </script>
    </body>
    </html>`
    w.Write([]byte(html))
}


//block {"blockNumber":1,"hash":"0xabc","txCount":4}
