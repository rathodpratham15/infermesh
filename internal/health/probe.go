// Package health holds the container health probe shared by the commands.
package health

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// Probe GETs /healthz on the address a server listens on (for example ":8080" or
// "0.0.0.0:9100"; an empty address means defaultPort). A wildcard bind address is
// probed over loopback. Distroless images have no shell or curl, so Docker
// HEALTHCHECK runs "<binary> healthcheck", which calls this.
func Probe(listenAddr, defaultPort string) error {
	host, port := "", defaultPort
	if listenAddr != "" {
		var err error
		if host, port, err = net.SplitHostPort(listenAddr); err != nil {
			return err
		}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz returned %d", resp.StatusCode)
	}
	return nil
}
