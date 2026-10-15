package charts

// The download clients the chart can run (templates/download-clients.yaml):
// what they render, the downloads claim they and acquire share, how the VPN
// reaches them, what acquire is told about them and where it copies to, the
// renders that must fail and where the operator reports them, and the two
// scripts their pods run.

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// The chart's two clients and what is fixed about them.
var chartClients = []struct {
	name, typ, protocol string
	port                int
}{
	{"qbittorrent", "qbittorrent", "torrent", 8080},
	{"nzbget", "nzbget", "usenet", 6789},
}

const (
	acquireMediaMount     = "/var/lib/katalog"
	acquireDownloadsMount = "/var/lib/acquire/downloads"
	downloadsClaim        = "acquire-downloads"
	// A tenth of the default 300Gi, in bytes and in NZBGet's megabytes.
	defaultReserve   = "32212254720"
	defaultReserveMB = "30720"
)

// TestChartDownloadClients: with both clients on (the CI values), each is a
// Deployment, a Service inside the namespace and a configuration claim, and
// both work on the one downloads claim, each in its folder, mounting nothing of
// the media storage. acquire reads that claim, writes only the arrivals of the
// media claim, and is told about exactly these clients, at their Services,
// with their passwords and folders. Only qBittorrent is assigned to the VPN.
func TestChartDownloadClients(t *testing.T) {
	objs := mustRender(t)
	vals := values(t)
	proxy := str(t, vals, "zaentrum.vpn.proxy")
	media := str(t, vals, "zaentrum.media.claimName")
	stored := secretData(t, objs)

	cm := mustFind(t, objs, "ConfigMap", "download-clients")
	for _, file := range []string{"configure-client.py", "nzbget-release.py"} {
		script, err := os.ReadFile(filepath.Join(chartDir, "files", file))
		if err != nil {
			t.Fatal(err)
		}
		if cm.Data[file] != string(script) {
			t.Errorf("ConfigMap/download-clients does not hold files/%s as it is", file)
		}
	}

	var pvc pvcSpec
	decodeSpec(t, mustFind(t, objs, "PersistentVolumeClaim", downloadsClaim), &pvc)
	if !reflect.DeepEqual(pvc.AccessModes, []string{"ReadWriteOnce"}) || pvc.Resources.Requests["storage"] != "300Gi" ||
		pvc.StorageClassName != "" || pvc.VolumeName != "" {
		t.Errorf("the downloads claim: %+v", pvc)
	}

	acquire := mustFind(t, objs, "Deployment", "acquire")
	aw := workloadOf(t, acquire)
	ac := containerOf(t, acquire)
	wantEnv(t, "acquire", ac, map[string]string{
		"ACQUIRE_ARRIVALS_ROOT":     acquireMediaMount + "/.work/incoming",
		"ACQUIRE_DOWNLOADS_RESERVE": defaultReserve,
	})
	wantMountOf(t, aw, ac, acquireMediaMount, media, "", true)
	wantMountOf(t, aw, ac, acquireMediaMount+"/.work/incoming", media, ".work/incoming", false)
	wantMountOf(t, aw, ac, acquireDownloadsMount, downloadsClaim, "", true)

	declared := map[string]map[string]any{}
	ref := envOf(ac, "ACQUIRE_MANAGED_CLIENTS")
	if ref == nil || ref.ValueFrom == nil || ref.ValueFrom.SecretKeyRef == nil {
		t.Fatal("acquire: ACQUIRE_MANAGED_CLIENTS must come from a secretKeyRef")
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(stored[ref.ValueFrom.SecretKeyRef.Name][ref.ValueFrom.SecretKeyRef.Key]), &list); err != nil {
		t.Fatalf("ACQUIRE_MANAGED_CLIENTS is not a JSON list: %v", err)
	}
	for _, c := range list {
		declared[c["id"].(string)] = c
	}
	if len(declared) != len(chartClients) {
		t.Errorf("acquire is told about %d clients, want %d: %v", len(declared), len(chartClients), list)
	}

	for _, cc := range chartClients {
		t.Run(cc.name, func(t *testing.T) {
			dep := mustFind(t, objs, "Deployment", cc.name)
			w := workloadOf(t, dep)
			pod := w.Template.Spec
			main := containerNamed(t, dep, cc.name)
			vpn := cc.name == "qbittorrent"

			if got := w.Template.Metadata.Labels["zaentrum.io/vpn"]; (got == "true") != vpn || (!vpn && got != "") {
				t.Errorf("pod label zaentrum.io/vpn = %q, want it only on a client through the VPN", got)
			}
			if _, ok := w.Selector.MatchLabels["zaentrum.io/vpn"]; ok {
				t.Error("the VPN label is in the selector, which cannot change")
			}
			if w.Strategy.Type != "Recreate" {
				t.Errorf("strategy %q: two instances on one configuration claim must never run at once", w.Strategy.Type)
			}
			if pod.SecurityContext.FSGroupChangePolicy != "OnRootMismatch" {
				t.Errorf("fsGroupChangePolicy %q: the downloads volume must not be chowned file by file on every start", pod.SecurityContext.FSGroupChangePolicy)
			}

			var svc serviceSpec
			decodeSpec(t, mustFind(t, objs, "Service", cc.name), &svc)
			if (svc.Type != "" && svc.Type != "ClusterIP") || len(svc.Ports) != 1 || svc.Ports[0].Port != cc.port {
				t.Errorf("Service: %+v, want ClusterIP on %d", svc, cc.port)
			}
			var cfg pvcSpec
			decodeSpec(t, mustFind(t, objs, "PersistentVolumeClaim", cc.name+"-config"), &cfg)
			if cfg.Resources.Requests["storage"] != "1Gi" {
				t.Errorf("configuration claim: %+v", cfg)
			}

			if len(pod.InitContainers) != 1 {
				t.Fatalf("want one init container, got %d", len(pod.InitContainers))
			}
			setup := pod.InitContainers[0]
			if setup.Image != main.Image || strings.Join(setup.Command, " ") != "python3 -I /setup/configure-client.py" {
				t.Errorf("init container %s runs %v in %s", setup.Name, setup.Command, setup.Image)
			}
			wantProxy := ""
			if vpn {
				wantProxy = proxy
			}
			env := map[string]string{
				"CLIENT": cc.name, "CLIENT_USERNAME": "acquire", "CLIENT_PORT": strconv.Itoa(cc.port),
				"CLIENT_PROXY": wantProxy, "CONFIG_DIR": "/config", "DOWNLOADS": "/downloads",
			}
			if vpn {
				env["CLIENT_SEED_RATIO"], env["CLIENT_SEED_MINUTES"] = "1", "4320"
			} else {
				env["CLIENT_DISK_SPACE_MB"] = defaultReserveMB
			}
			wantEnv(t, cc.name+" setup", setup, env)
			password := str(t, vals, "clients."+cc.name+".password")
			wantSecretEnv(t, setup, "CLIENT_PASSWORD", stored, password)

			// Every container of the pod: its folder of the downloads claim at
			// /downloads, writable, and nothing of the media claim.
			containers := append([]container{setup}, pod.Containers...)
			for _, c := range containers {
				for _, m := range c.VolumeMounts {
					if volumeClaim(w, m.Name) == media {
						t.Errorf("container %s mounts the media claim at %s", c.Name, m.MountPath)
					}
				}
				if c.Name != "release" || cc.name == "nzbget" {
					wantMountOf(t, w, c, "/downloads", downloadsClaim, cc.name, false)
				}
			}
			wantMountOf(t, w, main, "/config", cc.name+"-config", "", false)

			// NZBGet's pod deletes what acquire released.
			if cc.name == "nzbget" {
				rel := containerNamed(t, dep, "release")
				if rel.Image != main.Image || strings.Join(rel.Command, " ") != "python3 -I /setup/nzbget-release.py" {
					t.Errorf("release container runs %v in %s", rel.Command, rel.Image)
				}
				wantEnv(t, "release", rel, map[string]string{"CLIENT_USERNAME": "acquire", "CLIENT_PORT": "6789", "DOWNLOADS": "/downloads"})
				wantSecretEnv(t, rel, "CLIENT_PASSWORD", stored, password)
			} else if len(pod.Containers) != 1 {
				t.Errorf("%d containers, want qBittorrent alone", len(pod.Containers))
			}

			d := declared[cc.name]
			if d == nil {
				t.Fatalf("acquire is not told about %s", cc.name)
			}
			for k, v := range map[string]any{
				"type": cc.typ, "baseUrl": "http://" + cc.name + ":" + strconv.Itoa(cc.port), "auth": "basic",
				"username": "acquire", "secret": password, "category": "acquire",
				"remotePath": "/downloads/complete", "localPath": acquireDownloadsMount + "/" + cc.name + "/complete",
			} {
				if d[k] != v {
					t.Errorf("declared %s = %v, want %v", k, d[k], v)
				}
			}
			if p, _ := d["protocols"].([]any); len(p) != 1 || p[0] != cc.protocol {
				t.Errorf("declared protocols %v, want [%s]", d["protocols"], cc.protocol)
			}
		})
	}

	// The one writable mount of the media claim anywhere: acquire's arrivals.
	for _, o := range objs {
		if o.Kind != "Deployment" {
			continue
		}
		w := workloadOf(t, o)
		for _, c := range append(w.Template.Spec.InitContainers, w.Template.Spec.Containers...) {
			for _, m := range c.VolumeMounts {
				if volumeClaim(w, m.Name) == media && !m.ReadOnly && (o.Metadata.Name != "acquire" || m.SubPath != ".work/incoming") {
					t.Errorf("%s container %s mounts the media claim writable at %s", o.ref(), c.Name, m.MountPath)
				}
			}
		}
	}
}

// TestChartDownloadClientVariants: a platform without a VPN runs qBittorrent
// without one, also when the operator sets no vpn block; a reserve and storage
// class given are used; and with the clients off nothing of them is rendered,
// acquire keeps its whole view of the media claim read-only and is told about
// none, so it removes what it stored for an earlier render.
func TestChartDownloadClientVariants(t *testing.T) {
	for name, args := range map[string][]string{
		"no proxy":     {"--set", "zaentrum.vpn.proxy=", "--set", "clients.qbittorrent.vpn=false"},
		"no vpn block": {"--set", "zaentrum.vpn=null", "--set", "clients.qbittorrent.vpn=false"},
	} {
		t.Run(name, func(t *testing.T) {
			objs := mustRender(t, args...)
			guard := platformGuard(t, values(t), chartMeta(t))
			guard.VPN = ""
			if v := violations(objs, guard); len(v) > 0 {
				t.Errorf("the operator would refuse:\n  %s", strings.Join(v, "\n  "))
			}
			w := workloadOf(t, mustFind(t, objs, "Deployment", "qbittorrent"))
			if _, ok := w.Template.Metadata.Labels["zaentrum.io/vpn"]; ok {
				t.Error("assigned to the VPN without one")
			}
			wantEnv(t, "qbittorrent setup", w.Template.Spec.InitContainers[0], map[string]string{"CLIENT_PROXY": ""})
		})
	}

	t.Run("a downloads claim on a local volume", func(t *testing.T) {
		file := filepath.Join(chartDir, "ci", "local-downloads-values.yaml")
		out, err := helmTemplateOutput(t, release, file)
		if err != nil {
			t.Fatal(err)
		}
		objs, err := decodeManifests(out)
		if err != nil {
			t.Fatal(err)
		}
		var pvc pvcSpec
		decodeSpec(t, mustFind(t, objs, "PersistentVolumeClaim", downloadsClaim), &pvc)
		if pvc.StorageClassName != "local-storage" || pvc.VolumeName != "" {
			t.Errorf("the downloads claim: %+v", pvc)
		}
		wantEnv(t, "acquire", containerOf(t, mustFind(t, objs, "Deployment", "acquire")),
			map[string]string{"ACQUIRE_DOWNLOADS_RESERVE": "21474836480"})
		nz := workloadOf(t, mustFind(t, objs, "Deployment", "nzbget")).Template.Spec.InitContainers[0]
		wantEnv(t, "nzbget setup", nz, map[string]string{"CLIENT_DISK_SPACE_MB": "20480"})
		qb := workloadOf(t, mustFind(t, objs, "Deployment", "qbittorrent")).Template.Spec.InitContainers[0]
		wantEnv(t, "qbittorrent setup", qb, map[string]string{"CLIENT_SEED_RATIO": "2.5", "CLIENT_SEED_MINUTES": "-1"})
	})

	t.Run("no clients", func(t *testing.T) {
		objs := mustRender(t, "--set", "clients.qbittorrent.enabled=false", "--set", "clients.nzbget.enabled=false")
		for _, o := range objs {
			switch o.ref() {
			case "Deployment/qbittorrent", "Deployment/nzbget", "Service/qbittorrent", "Service/nzbget",
				"PersistentVolumeClaim/qbittorrent-config", "PersistentVolumeClaim/nzbget-config",
				"PersistentVolumeClaim/" + downloadsClaim, "ConfigMap/download-clients":
				t.Errorf("%s rendered with the clients off", o.ref())
			}
		}
		acquire := mustFind(t, objs, "Deployment", "acquire")
		ac := containerOf(t, acquire)
		for _, e := range []string{"ACQUIRE_ARRIVALS_ROOT", "ACQUIRE_DOWNLOADS_RESERVE"} {
			if envOf(ac, e) != nil {
				t.Errorf("acquire: %s set with no client to copy from", e)
			}
		}
		for _, m := range ac.VolumeMounts {
			if !m.ReadOnly && m.Name != "kafka-cert" {
				t.Errorf("acquire mounts %s writable with no client to copy from", m.MountPath)
			}
		}
		for _, v := range workloadOf(t, acquire).Template.Spec.Volumes {
			if src, _ := v["persistentVolumeClaim"].(map[string]any); src != nil && src["readOnly"] != true {
				t.Errorf("acquire's claim %v is not read-only", src["claimName"])
			}
		}
		keys := secretData(t, objs)["acquire-secrets"]
		if keys["download-clients"] != "[]" {
			t.Errorf("download-clients = %q, want []", keys["download-clients"])
		}
		for _, k := range []string{"qbittorrent-password", "nzbget-password"} {
			if _, ok := keys[k]; ok {
				t.Errorf("Secret/acquire-secrets: %s rendered for a client that is off", k)
			}
		}
	})

	// A claim that names its volume renders, for plain Helm, but the operator
	// refuses it: under the operator, the PV's claimRef binds it instead.
	t.Run("volumeName", func(t *testing.T) {
		objs := mustRender(t, "--set", "clients.downloads.volumeName=pv-node1-acquire-downloads",
			"--set", "clients.downloads.storageClassName=local-storage")
		var pvc pvcSpec
		decodeSpec(t, mustFind(t, objs, "PersistentVolumeClaim", downloadsClaim), &pvc)
		if pvc.VolumeName != "pv-node1-acquire-downloads" {
			t.Errorf("volumeName %q", pvc.VolumeName)
		}
		got := violations(objs, platformGuard(t, values(t), chartMeta(t)))
		if len(got) != 1 || !strings.Contains(got[0], "PersistentVolumeClaim/"+downloadsClaim+": PVC spec.volumeName not allowed") {
			t.Errorf("the operator's verdict: %v", got)
		}
	})
}

// TestChartDownloadClientRefusals: the renders that must fail do, and the
// operator, which reports a template failure by its location alone, reports
// each in a file whose name says what is wrong.
func TestChartDownloadClientRefusals(t *testing.T) {
	loc := regexp.MustCompile(`\(([^()]+?\.(?:yaml|yml|tpl|txt):\d+(?::\d+)?)\)`)
	for _, c := range []struct {
		name string
		args []string
		file string
		says string
	}{
		{"qBittorrent through a VPN the platform does not have", []string{"--set", "zaentrum.vpn.proxy="},
			"templates/qbittorrent-vpn-needs-the-platform-vpn.yaml:", "set clients.qbittorrent.vpn to false"},
		{"no vpn block", []string{"--set", "zaentrum.vpn=null"},
			"templates/qbittorrent-vpn-needs-the-platform-vpn.yaml:", "zaentrum.vpn.proxy is empty"},
		{"a proxy that is not SOCKS5", []string{"--set", "zaentrum.vpn.proxy=http://proxy.example.org:3128"},
			"templates/qbittorrent-vpn-needs-the-platform-vpn.yaml:", "socks5h://host:port"},
		{"NZBGet through the VPN", []string{"--set", "clients.nzbget.vpn=true"},
			"templates/nzbget-cannot-use-the-vpn.yaml:", "NZBGet has no SOCKS5 proxy"},
		{"a client without the media claim", []string{"--set", "zaentrum.media.claimName="},
			"templates/download-clients-need-the-media-claim.yaml:", "acquire copies finished downloads into it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := helmTemplateOutput(t, release, testValues, c.args...)
			if err == nil {
				t.Fatal("rendered")
			}
			m := loc.FindStringSubmatch(err.Error())
			if m == nil || !strings.Contains(m[1], c.file) {
				t.Errorf("want a template error located in %s, got %v", c.file, err)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the message does not say %q: %v", c.says, err)
			}
		})
	}

	// Rendered without the operator, a missing password says the operator
	// would have generated it; the schema refuses a short or padded one, and
	// a size the chart cannot read.
	b, err := yaml.Marshal(without(values(t), "clients.nzbget.password"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = helmTemplateOutput(t, release, file)
	if err == nil || !strings.Contains(err.Error(), "clients.nzbget.password is required") {
		t.Errorf("without a password: %v", err)
	}
	for _, args := range [][]string{
		{"--set-string", "clients.qbittorrent.password=short"},
		{"--set-string", "clients.qbittorrent.password= padded-password "},
		{"--set-string", "clients.downloads.size=1.5Ti"},
		{"--set-string", "clients.downloads.reserve=10%"},
	} {
		if _, err := helmTemplateOutput(t, release, testValues, args...); err == nil || !strings.Contains(err.Error(), "schema") {
			t.Errorf("%v: want a schema error, got %v", args, err)
		}
	}
}

// TestChartCIValues: every CI values file renders and passes the guardrails
// against the platform it describes.
func TestChartCIValues(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(chartDir, "ci", "*-values.yaml"))
	if err != nil || len(files) < 2 {
		t.Fatalf("CI values files: %v %v", files, err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			out, err := helmTemplateOutput(t, release, file)
			if err != nil {
				t.Fatal(err)
			}
			objs, err := decodeManifests(out)
			if err != nil {
				t.Fatal(err)
			}
			if v := violations(objs, platformGuard(t, valuesOf(t, file), chartMeta(t))); len(v) > 0 {
				t.Errorf("the operator would refuse:\n  %s", strings.Join(v, "\n  "))
			}
			checkSecretRefs(t, objs)
		})
	}
}

// ── the scripts ────────────────────────────────────────────────────────────

func python(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	return p
}

func runScript(t *testing.T, script string, args []string, env ...string) (string, error) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join(chartDir, "files", script))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python(t), append([]string{"-I", path}, args...)...)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	return out.String(), err
}

// TestClientSetupScript runs files/configure-client.py the way the init
// containers do, on the configuration qBittorrent's image starts from and on
// NZBGet's template: it sets the keys the chart owns, leaves the rest, makes
// the folders, refuses NZBGet a proxy, and changes nothing on a second start.
// Needs python3 (skipped without it).
func TestClientSetupScript(t *testing.T) {
	python(t)
	t.Run("qbittorrent", func(t *testing.T) {
		dir := t.TempDir()
		conf := filepath.Join(dir, "config", "qBittorrent", "qBittorrent.conf")
		if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
			t.Fatal(err)
		}
		// What the image's entrypoint copies on the first start: never
		// migrated by qBittorrent, with legacy keys of its own.
		seed, err := os.ReadFile(filepath.Join("testdata", "qBittorrent.conf"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(conf, seed, 0o644); err != nil {
			t.Fatal(err)
		}
		downloads := filepath.Join(dir, "downloads")
		envWith := func(proxy string) []string {
			return []string{"CLIENT=qbittorrent", "CLIENT_USERNAME=acquire", "CLIENT_PASSWORD=pw-in-the-secret",
				"CLIENT_PORT=8080", "CLIENT_PROXY=" + proxy, "CLIENT_SEED_RATIO=1.5", "CLIENT_SEED_MINUTES=4320",
				"CONFIG_DIR=" + filepath.Join(dir, "config"), "DOWNLOADS=" + downloads}
		}
		env := envWith("socks5h://vpn.vpn.svc.cluster.local:1080")
		if out, err := runScript(t, "configure-client.py", nil, env...); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		body := readFile(t, conf)
		for _, want := range []string{
			"[BitTorrent]", `Session\DefaultSavePath=` + downloads + `/complete`, `Session\TempPath=` + downloads + `/incomplete`,
			`Session\TempPathEnabled=true`, `Session\Preallocation=true`,
			`Session\GlobalMaxRatio=1.5`, `Session\GlobalMaxSeedingMinutes=4320`, `Session\ShareLimitAction=Stop`,
			`Session\ProxyPeerConnections=true`, `Session\LSDEnabled=false`, `Session\ProxyHostnameLookup=true`,
			"[Network]", `Proxy\Type=SOCKS5`, `Proxy\IP=vpn.vpn.svc.cluster.local`, `Proxy\Port=1080`,
			`Proxy\AuthEnabled=false`, `Proxy\HostnameLookupEnabled=true`, `Proxy\Profiles\BitTorrent=true`,
			`Proxy\Profiles\RSS=true`, `Proxy\Profiles\Misc=true`, `PortForwardingEnabled=false`,
			"[Preferences]", `WebUI\Username=acquire`, `WebUI\LocalHostAuth=true`,
			`WebUI\AuthSubnetWhitelistEnabled=false`, `WebUI\Address=*`, `WebUI\Port=8080`,
			`Connection\ResolvePeerHostNames=false`,
			// The image's own settings stay.
			`Session\DiskIOType=SimplePreadPwrite`, `Accepted=true`,
		} {
			if !strings.Contains(body, want+"\n") {
				t.Errorf("qBittorrent.conf lacks %s", want)
			}
		}
		if strings.Contains(body, `Connection\UPnP=`) {
			t.Error(`Connection\UPnP is still there: qBittorrent's migration would copy it over PortForwardingEnabled`)
		}
		m := regexp.MustCompile(`WebUI\\Password_PBKDF2="@ByteArray\(([^:]+):([^)]+)\)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no WebUI password in:\n%s", body)
		}
		salt, _ := base64.StdEncoding.DecodeString(m[1])
		key, _ := base64.StdEncoding.DecodeString(m[2])
		derived, err := pbkdf2.Key(sha512.New, "pw-in-the-secret", salt, 100000, 64)
		if err != nil || len(salt) != 16 || !bytes.Equal(derived, key) {
			t.Error("WebUI\\Password_PBKDF2 does not verify as qBittorrent's PBKDF2-SHA512")
		}
		for _, sub := range []string{"incomplete", "complete"} {
			if info, err := os.Stat(filepath.Join(downloads, sub)); err != nil || !info.IsDir() {
				t.Errorf("the folder %s was not made: %v", sub, err)
			}
		}
		if out, err := runScript(t, "configure-client.py", nil, env...); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if again := readFile(t, conf); again != body {
			t.Errorf("a second start changed the file:\n%s", again)
		}
		if out, err := runScript(t, "configure-client.py", nil, envWith("")...); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if body := readFile(t, conf); !strings.Contains(body, `Proxy\Type=None`) {
			t.Error("the proxy stayed on without the VPN")
		}
	})

	t.Run("nzbget", func(t *testing.T) {
		dir := t.TempDir()
		conf := filepath.Join(dir, "config", "nzbget.conf")
		if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
			t.Fatal(err)
		}
		seed := "# The template's options, as the image's entrypoint leaves them\nMainDir=/config\n" +
			"DestDir=${MainDir}/completed\nInterDir=${MainDir}/intermediate\n# ControlUsername=commented\n" +
			"ControlIP=0.0.0.0\nControlPort=6789\nControlUsername=\nControlPassword=\nAuthorizedIP=127.0.0.1\n" +
			"DiskSpace=250\nServer1.Host=news.example.org\n"
		if err := os.WriteFile(conf, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		downloads := filepath.Join(dir, "downloads")
		env := []string{"CLIENT=nzbget", "CLIENT_USERNAME=acquire", "CLIENT_PASSWORD=usenet-pw", "CLIENT_PORT=6789",
			"CLIENT_DISK_SPACE_MB=30720", "CONFIG_DIR=" + filepath.Join(dir, "config"), "DOWNLOADS=" + downloads}
		if out, err := runScript(t, "configure-client.py", nil, env...); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		want := "# The template's options, as the image's entrypoint leaves them\nMainDir=/config\n" +
			"DestDir=" + downloads + "/complete\nInterDir=" + downloads + "/incomplete\n# ControlUsername=commented\n" +
			"ControlIP=0.0.0.0\nControlPort=6789\nControlUsername=acquire\nControlPassword=usenet-pw\nAuthorizedIP=\n" +
			"DiskSpace=30720\nServer1.Host=news.example.org\n"
		if got := readFile(t, conf); got != want {
			t.Errorf("nzbget.conf:\n%s\nwant:\n%s", got, want)
		}
		if info, err := os.Stat(conf); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("nzbget.conf holds the password and must be 0600: %v %v", info.Mode(), err)
		}
		if out, err := runScript(t, "configure-client.py", nil, append(env, "CLIENT_PROXY=socks5h://vpn:1080")...); err == nil || !strings.Contains(out, "no SOCKS5 proxy") {
			t.Errorf("NZBGet given a proxy: %v %s", err, out)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		dir := t.TempDir()
		base := []string{"CLIENT=qbittorrent", "CLIENT_USERNAME=acquire", "CLIENT_PORT=8080", "CONFIG_DIR=" + dir,
			"DOWNLOADS=" + dir}
		for name, extra := range map[string][]string{
			"control character": {"CLIENT_PASSWORD=pw\tpw"},
			"no password":       {"CLIENT_PASSWORD="},
			"http proxy":        {"CLIENT_PASSWORD=pw", "CLIENT_PROXY=http://proxy:3128"},
			"proxy credentials": {"CLIENT_PASSWORD=pw", "CLIENT_PROXY=socks5h://user:secret@vpn:1080"},
			"unknown client":    {"CLIENT_PASSWORD=pw", "CLIENT=other"},
			"ratio":             {"CLIENT_PASSWORD=pw", "CLIENT_SEED_RATIO=lots"},
		} {
			if out, err := runScript(t, "configure-client.py", nil, append(append([]string{}, base...), extra...)...); err == nil {
				t.Errorf("%s: accepted: %s", name, out)
			} else if strings.Contains(out, "secret") {
				t.Errorf("%s: the refusal echoes a credential: %s", name, out)
			}
		}
	})
}

// fakeNZBGet answers the JSON-RPC calls of files/nzbget-release.py.
type fakeNZBGet struct {
	mu      sync.Mutex
	history []map[string]any
	deleted []any
}

func (f *fakeNZBGet) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		user, pass, ok := r.BasicAuth()
		if !ok || user != "acquire" || pass != "usenet-pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case body.Method == "history":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": f.history})
		case body.Method == "editqueue" && body.Params[0] == "HistoryFinalDelete":
			f.deleted = append(f.deleted, body.Params[3].([]any)...)
			_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "unexpected " + body.Method}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestNZBGetReleaseScript: a pass of files/nzbget-release.py deletes the
// folder of each record acquire released, then the record; it leaves records
// acquire did not release, and anything outside the finished downloads.
func TestNZBGetReleaseScript(t *testing.T) {
	python(t)
	downloads := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(downloads, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "Film.mkv"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	released, kept, outside := mk("complete/acquire/Released"), mk("complete/acquire/Kept"), mk("incomplete/Partial")
	mark := []any{map[string]any{"Name": "acquire:release", "Value": "yes"}}
	f := &fakeNZBGet{history: []map[string]any{
		{"NZBID": 1, "DestDir": released, "FinalDir": "", "Parameters": mark},
		{"NZBID": 2, "DestDir": kept, "FinalDir": "", "Parameters": []any{}},
		{"NZBID": 3, "DestDir": outside, "FinalDir": "", "Parameters": mark},
	}}
	srv := f.server(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"CLIENT_USERNAME=acquire", "CLIENT_PASSWORD=usenet-pw", "CLIENT_PORT=" + u.Port(), "DOWNLOADS=" + downloads}
	out, err := runScript(t, "nzbget-release.py", []string{"--once"}, env...)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(released); !os.IsNotExist(err) {
		t.Errorf("the released folder is still there: %v", err)
	}
	for _, p := range []string{kept, outside} {
		if _, err := os.Stat(filepath.Join(p, "Film.mkv")); err != nil {
			t.Errorf("%s was deleted: %v", p, err)
		}
	}
	got := append([]any{}, f.deleted...)
	sort.Slice(got, func(i, j int) bool { return got[i].(float64) < got[j].(float64) })
	if !reflect.DeepEqual(got, []any{float64(1)}) {
		t.Errorf("records removed %v, want [1]", got)
	}
	if !strings.Contains(out, "outside") {
		t.Errorf("the record pointing outside the finished downloads is not reported: %s", out)
	}

	env[1] = "CLIENT_PASSWORD=wrong"
	if out, err := runScript(t, "nzbget-release.py", []string{"--once"}, env...); err == nil || !strings.Contains(out, "could not be asked") {
		t.Errorf("a refused login: %v %s", err, out)
	}
}

// wantMountOf: the container mounts path from the volume of claim, at subPath,
// read-only or writable as asked.
func wantMountOf(t *testing.T, w workloadSpec, c container, path, claim, subPath string, readOnly bool) {
	t.Helper()
	for _, m := range c.VolumeMounts {
		if m.MountPath != path {
			continue
		}
		if got := volumeClaim(w, m.Name); got != claim || m.SubPath != subPath || m.ReadOnly != readOnly {
			t.Errorf("container %s: %s is %+v of claim %q, want claim %q at subPath %q, read-only %v", c.Name, path, m, got, claim, subPath, readOnly)
		}
		return
	}
	t.Errorf("container %s: nothing is mounted at %s", c.Name, path)
}

// wantSecretEnv: the variable comes from the chart's Secret and holds value.
func wantSecretEnv(t *testing.T, c container, name string, stored map[string]map[string]string, value string) {
	t.Helper()
	e := envOf(c, name)
	if e == nil || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil ||
		stored[e.ValueFrom.SecretKeyRef.Name][e.ValueFrom.SecretKeyRef.Key] != value {
		t.Errorf("container %s: %s must come from the chart's Secret and hold its value", c.Name, name)
	}
}

// volumeClaim is the claim behind a pod's volume, "" when it is no claim.
func volumeClaim(w workloadSpec, volume string) string {
	for _, v := range w.Template.Spec.Volumes {
		if v["name"] != volume {
			continue
		}
		src, _ := v["persistentVolumeClaim"].(map[string]any)
		name, _ := src["claimName"].(string)
		return name
	}
	return ""
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
