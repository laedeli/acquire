package config

import "testing"

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
