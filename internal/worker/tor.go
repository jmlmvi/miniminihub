package worker

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// V002 P2 (NEWNYM) — pilotage du daemon tor local via son ControlPort pour
// renouveler les circuits (nouvelle IP de sortie) à la demande du parent.
const (
	// torControlAddr = ControlPort du daemon tor local sur la VM (option A).
	torControlAddr = "127.0.0.1:9051"
	// torCookiePath = fichier de cookie d'auth écrit par tor quand
	// CookieAuthentication 1 (défaut paquet Debian). Doit être lisible par
	// l'utilisateur qui exécute l'agent (groupe debian-tor / GroupReadable).
	torCookiePath = "/run/tor/control.authcookie"
)

// rotateTor envoie SIGNAL NEWNYM au daemon tor local via le ControlPort,
// forçant de nouveaux circuits → nouvelle IP de sortie TOR. Authentification
// par cookie (AUTHENTICATE <hexcookie>). Adresse et chemin du cookie
// surchargeables par MMH_TOR_CONTROL_ADDR / MMH_TOR_COOKIE_PATH.
func rotateTor() error {
	addr := envOr("MMH_TOR_CONTROL_ADDR", torControlAddr)
	cookiePath := envOr("MMH_TOR_COOKIE_PATH", torCookiePath)

	cookie, err := os.ReadFile(cookiePath)
	if err != nil {
		return fmt.Errorf("lecture cookie tor %s: %w", cookiePath, err)
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial ControlPort %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	r := bufio.NewReader(conn)
	if err := torCmd(conn, r, "AUTHENTICATE "+hex.EncodeToString(cookie)); err != nil {
		return fmt.Errorf("AUTHENTICATE: %w", err)
	}
	if err := torCmd(conn, r, "SIGNAL NEWNYM"); err != nil {
		return fmt.Errorf("SIGNAL NEWNYM: %w", err)
	}
	_, _ = conn.Write([]byte("QUIT\r\n"))
	return nil
}

// torCmd envoie une ligne de commande au ControlPort et vérifie le code 250.
func torCmd(conn net.Conn, r *bufio.Reader, cmd string) error {
	if _, err := conn.Write([]byte(cmd + "\r\n")); err != nil {
		return err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "250") {
		return fmt.Errorf("réponse tor inattendue: %q", line)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
