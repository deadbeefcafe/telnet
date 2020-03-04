package telnet

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
)

type Telnet struct {
	rxmu         sync.Mutex
	rxbuf        bytes.Buffer
	rxcond       *sync.Cond
	address      string
	mu           sync.Mutex
	client_count int
	clients      map[net.Conn]bool
	Motd         string
	CtrlDclose   bool
	log          *log.Logger
}

func (t *Telnet) rxchar(ch byte) {
	t.rxmu.Lock()
	t.rxbuf.WriteByte(ch)
	t.rxmu.Unlock()
	t.rxcond.Signal()

}

func (t *Telnet) handleSocket(conn net.Conn) {
	const (
		S_DATA = iota
		S_IAC
		S_WILL
		S_WONT
		S_DO
		S_DONT
		S_WILL_WONT_DO_DONT
		S_SE
		S_SB

		IAC  = 255
		SB   = 250
		SE   = 240
		WILL = 251
		WONT = 252
		DO   = 253
		DONT = 254

		EOT = 4

		OPT_ECHO              = 1
		OPT_SUPPRESS_GO_AHEAD = 3
	)

	buf := make([]byte, 1024)

	if t.Motd != "" {
		conn.Write([]byte(t.Motd))
	}

	// we will handle echo on this end
	conn.Write([]byte{IAC, WILL, OPT_ECHO})
	conn.Write([]byte{IAC, WILL, OPT_SUPPRESS_GO_AHEAD})

	var state int = S_DATA

	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				fmt.Println("Error reading:", err.Error())
			}
			break
		}
		// implement just enough of the telnet protocol to discard all
		// the options coming from the client
		for x := 0; x < n; x++ {
			ch := buf[x]
			//fmt.Printf("state %d got char %c [%d]\n", state, ch, ch)
			switch state {
			case S_DATA:
				switch ch {
				case IAC:
					state = S_IAC
				case EOT:
					if t.CtrlDclose {
						t.clientClose(conn)
					} else {
						t.rxchar(ch)
					}
					return
				default:
					t.rxchar(ch)
					//t.Write([]byte{ch})
				}
			case S_IAC:
				switch ch {
				case WILL, WONT, DO, DONT:
					state = S_WILL_WONT_DO_DONT
				case SB:
					state = S_SB
				case IAC:
					t.Write([]byte{ch})
				}
			case S_WILL_WONT_DO_DONT:
				state = S_DATA
				// discard option
			case S_SB:
				if ch == IAC {
					state = S_SE
				}
				/* discard byte */
			case S_SE:
				if ch == IAC {
					state = S_SB
				} else {
					state = S_DATA
				}
			}
		}
	}

	t.clientClose(conn)
}

func (t *Telnet) clientClose(conn net.Conn) {
	t.mu.Lock()
	delete(t.clients, conn)
	t.client_count--
	t.log.Printf("Telnet client %v disconnected. clients: %d", conn.RemoteAddr(), t.client_count)
	t.mu.Unlock()
	conn.Close()
}

func (t *Telnet) listenSocket() {

	l, err := net.Listen("tcp", t.address)
	if err != nil {
		t.log.Printf("Telnet listen failed %v\n", err)
		return
	}
	defer l.Close()
	t.log.Printf("Telnet server listnening on %s ...\n", t.address)
	for {
		// Listen for an incoming connection.
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting: ", err.Error())
			return
		}

		t.mu.Lock()
		t.clients[conn] = true
		t.client_count++
		t.mu.Unlock()
		// Handle connections in a new goroutine.
		t.log.Printf("Telnet client %v connected. clients: %d", conn.RemoteAddr(), t.client_count)
		go t.handleSocket(conn)
	}
}

func (t *Telnet) SetLogger(logger *log.Logger) {
	t.log = logger
}

func New(address string) *Telnet {
	t := Telnet{
		rxcond:  &sync.Cond{L: &sync.Mutex{}},
		clients: make(map[net.Conn]bool),
		address: address,
		log:     log.New(os.Stdout, "", log.LstdFlags),
	}
	go t.listenSocket()
	return &t
}

// Read returns data from connected clients.  It blocks
// until data is available
func (t *Telnet) Read(p []byte) (n int, err error) {
	t.rxmu.Lock()
	n = t.rxbuf.Len()
	t.rxmu.Unlock()
	if n == 0 {
		t.rxcond.L.Lock()
		t.rxcond.Wait()
		t.rxcond.L.Unlock()
	}
	t.rxmu.Lock()
	n, err = t.rxbuf.Read(p)
	t.rxmu.Unlock()
	return
}

// Write writes data to all connected clients
func (t *Telnet) Write(p []byte) (n int, err error) {
	t.mu.Lock()
	for client, _ := range t.clients {
		n, err = client.Write(p)
		if err != nil {
			return
		}
	}
	t.mu.Unlock()
	return
}

func (t *Telnet) Close() error {
	return nil
}
