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

// ACQUIRE_MANAGED_CLIENTS unset leaves the stored clients alone; set, even to
// nothing, is the deployment's declaration.
func TestManagedClientsDeclaration(t *testing.T) {
	t.Run("unset declares nothing", func(t *testing.T) {
		t.Setenv("ACQUIRE_MANAGED_CLIENTS", "")
		os.Unsetenv("ACQUIRE_MANAGED_CLIENTS")
		if c := Load(); c.ManagedClientsSet || c.ManagedClients != "" {
			t.Errorf("unset: set=%v %q", c.ManagedClientsSet, c.ManagedClients)
		}
	})
	t.Run("empty declares none", func(t *testing.T) {
		t.Setenv("ACQUIRE_MANAGED_CLIENTS", " ")
		if c := Load(); !c.ManagedClientsSet || c.ManagedClients != "" {
			t.Errorf("empty: set=%v %q", c.ManagedClientsSet, c.ManagedClients)
		}
	})
	t.Run("a list", func(t *testing.T) {
		t.Setenv("ACQUIRE_MANAGED_CLIENTS", `[{"id":"nzbget"}]`)
		if c := Load(); !c.ManagedClientsSet || c.ManagedClients != `[{"id":"nzbget"}]` {
			t.Errorf("list: set=%v %q", c.ManagedClientsSet, c.ManagedClients)
		}
	})
}

func TestCopyOnImportSettings(t *testing.T) {
	t.Setenv("ACQUIRE_ARRIVALS_ROOT", "/var/lib/katalog/.work/incoming/")
	t.Setenv("ACQUIRE_DOWNLOADS_RESERVE", "32212254720")
	c := Load()
	if c.ArrivalsRoot != "/var/lib/katalog/.work/incoming" || c.DownloadsReserveBytes != 30<<30 {
		t.Errorf("arrivals %q, reserve %d", c.ArrivalsRoot, c.DownloadsReserveBytes)
	}
	t.Setenv("ACQUIRE_DOWNLOADS_RESERVE", "30Gi")
	if got := Load().DownloadsReserveBytes; got != 0 {
		t.Errorf("a reserve that is no byte count = %d, want 0", got)
	}
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
