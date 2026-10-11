package acp_test

import (
	"bufio"
	"net"
	"testing"
)

type rawAgent struct {
	conn net.Conn
	br   *bufio.Reader
}

func pipe() (net.Conn, *rawAgent) {
	client, server := net.Pipe()
	return client, &rawAgent{conn: server, br: bufio.NewReader(server)}
}

func (r *rawAgent) send(t *testing.T, line string) {
	t.Helper()
	if _, err := r.conn.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
}

func (r *rawAgent) recv(t *testing.T) string {
	t.Helper()
	line, err := r.br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return line
}
