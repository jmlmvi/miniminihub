package worker

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTorControl démarre un faux ControlPort tor sur une adresse locale. Il
// répond "250 OK" aux commandes attendues (AUTHENTICATE, SIGNAL NEWNYM) et
// capture les lignes reçues. reply250 permet de forcer un refus pour tester
// le chemin d'erreur.
func fakeTorControl(t *testing.T, reply250 bool) (addr string, received *[]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := &[]string{}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			*got = append(*got, line)
			if strings.HasPrefix(line, "QUIT") {
				_, _ = conn.Write([]byte("250 closing connection\r\n"))
				return
			}
			if reply250 {
				_, _ = conn.Write([]byte("250 OK\r\n"))
			} else {
				_, _ = conn.Write([]byte("515 Authentication failed\r\n"))
			}
		}
	}()
	return ln.Addr().String(), got
}

func writeCookie(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "control.authcookie")
	// 32 octets de cookie factice (comme tor).
	if err := os.WriteFile(p, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
	return p
}

func TestRotateTor_OK(t *testing.T) {
	addr, got := fakeTorControl(t, true)
	t.Setenv("MMH_TOR_CONTROL_ADDR", addr)
	t.Setenv("MMH_TOR_COOKIE_PATH", writeCookie(t))

	if err := rotateTor(); err != nil {
		t.Fatalf("rotateTor() erreur inattendue: %v", err)
	}
	// Vérifie que la séquence AUTHENTICATE puis SIGNAL NEWNYM a bien été envoyée.
	joined := strings.Join(*got, "|")
	if !strings.Contains(joined, "AUTHENTICATE ") {
		t.Errorf("AUTHENTICATE manquant, reçu: %v", *got)
	}
	if !strings.Contains(joined, "SIGNAL NEWNYM") {
		t.Errorf("SIGNAL NEWNYM manquant, reçu: %v", *got)
	}
}

func TestRotateTor_AuthRefused(t *testing.T) {
	addr, _ := fakeTorControl(t, false)
	t.Setenv("MMH_TOR_CONTROL_ADDR", addr)
	t.Setenv("MMH_TOR_COOKIE_PATH", writeCookie(t))

	if err := rotateTor(); err == nil {
		t.Fatal("rotateTor() aurait dû échouer sur refus d'authentification")
	}
}

func TestRotateTor_NoCookie(t *testing.T) {
	addr, _ := fakeTorControl(t, true)
	t.Setenv("MMH_TOR_CONTROL_ADDR", addr)
	t.Setenv("MMH_TOR_COOKIE_PATH", filepath.Join(t.TempDir(), "absent.cookie"))

	if err := rotateTor(); err == nil {
		t.Fatal("rotateTor() aurait dû échouer si le cookie est absent")
	}
}
