package main

import (
	"bufio"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// WINDOWS IPC

type IPC struct {
	pipePath string
	incoming chan string
	listener net.Listener
}

func NewIPC() *IPC {
	return &IPC{
		pipePath: `\\.\pipe\SqueezerIPC`,
		incoming: make(chan string, 100),
		listener: nil,
	}
}

// TryBecomePrimary returns true if the process owns the pipe.
func (ipc *IPC) TryBecomePrimary() bool {
	listener, err := winio.ListenPipe(ipc.pipePath, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;AU)"})
	if err != nil {
		return false
	}

	ipc.listener = listener
	go ipc.acceptLoop()

	return true
}

func (ipc *IPC) acceptLoop() {
	defer ipc.listener.Close()
	for {
		conn, err := ipc.listener.Accept()
		if err != nil {
			return
		}
		go ipc.readConn(conn)
	}
}

func (ipc *IPC) readConn(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		ipc.incoming <- sc.Text()
	}
	_ = sc.Err()
}

// Send forwards paths to the primary.
func (ipc *IPC) Send(paths []string) error {
	conn, err := winio.DialPipe(ipc.pipePath, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, p := range paths {
		fmt.Fprintln(conn, p)
	}
	return nil
}

// Incoming returns the channel of received paths.
func (ipc *IPC) Incoming() <-chan string {
	return ipc.incoming
}
