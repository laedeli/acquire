package config

import (
	"os"
	"testing"
)

// KAFKA_CERT_DIR is the one setting where empty differs from unset: empty
// selects plaintext brokers, unset keeps the certificate mount.
func TestKafkaCertDir(t *testing.T) {
	t.Run("unset keeps the mount", func(t *testing.T) {
		t.Setenv("KAFKA_CERT_DIR", "") // restores the variable afterwards
		os.Unsetenv("KAFKA_CERT_DIR")
		if got := Load().KafkaCertDir; got != "/etc/kafka-cert" {
			t.Errorf("KafkaCertDir = %q, want /etc/kafka-cert", got)
		}
	})
	t.Run("empty means plaintext", func(t *testing.T) {
		t.Setenv("KAFKA_CERT_DIR", "")
		if got := Load().KafkaCertDir; got != "" {
			t.Errorf("KafkaCertDir = %q, want empty", got)
		}
	})
	t.Run("a directory", func(t *testing.T) {
		t.Setenv("KAFKA_CERT_DIR", " /run/kafka ")
		if got := Load().KafkaCertDir; got != "/run/kafka" {
			t.Errorf("KafkaCertDir = %q, want /run/kafka", got)
		}
	})
}

func TestClusterDomainFromResolvConf(t *testing.T) {
	cases := map[string]string{
		// What Kubernetes writes into a pod.
		"search media.svc.cluster.local svc.cluster.local cluster.local\nnameserver 172.30.0.10\noptions ndots:5\n": "cluster.local",
		"search media.svc.cluster.example. svc.cluster.example. cluster.example. lan\n":                             "cluster.example",
		// A laptop or a plain container: no cluster search domains.
		"nameserver 192.168.1.1\nsearch lan\n": "",
		"":                                     "",
		"# search svc.cluster.local\n":         "",
	}
	for in, want := range cases {
		if got := clusterDomainFrom(in); got != want {
			t.Errorf("clusterDomainFrom(%q) = %q, want %q", in, got, want)
		}
	}
}
