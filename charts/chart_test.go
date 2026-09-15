// Package charts tests the addon chart the way the zaentrum operator sees it:
// rendered by helm with a full zaentrum block (acquire/ci/test-values.yaml),
// then held to the operator's guardrails (guardrails_test.go). A chart the
// operator would refuse fails here first.
//
// The chart tests need the helm CLI (HELM, else helm on PATH). They skip
// without it, unless REQUIRE_HELM is set, as the workflows do.
package charts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	chartDir   = "acquire"
	testValues = "acquire/ci/test-values.yaml"
	namespace  = "zaentrum-example"
	// release is the one name the addon installs under.
	release = "acquire"
)

// TestChartGuardrails: the render passes every guardrail, sets the pod security
// the guardrails ask for explicitly, carries the addon annotations, renders both
// components, and keeps every secret input out of the pod specs.
func TestChartGuardrails(t *testing.T) {
	objs := mustRender(t)
	vals := values(t)
	meta := chartMeta(t)

	for _, v := range violations(objs, platformGuard(t, vals, meta)) {
		t.Errorf("the operator would refuse: %s", v)
	}
	if meta.Annotations["zaentrum.io/addon"] != "true" {
		t.Errorf(`Chart.yaml: annotation zaentrum.io/addon must be "true"`)
	}
	if got := meta.Annotations["zaentrum.io/primary"]; got != "acquire" {
		t.Errorf("Chart.yaml: annotation zaentrum.io/primary = %q, want acquire", got)
	}

	for _, name := range []string{"acquire", "download-gateway"} {
		dep, svc := find(objs, "Deployment", name), find(objs, "Service", name)
		if dep == nil || svc == nil {
			t.Errorf("want Deployment/%s and Service/%s, rendered %v", name, name, refs(objs))
			continue
		}
		w := workloadOf(t, *dep)
		labels := w.Template.Metadata.Labels
		if !selects(w.Selector.MatchLabels, labels) {
			t.Errorf("Deployment/%s: selector %v does not select its pods %v", name, w.Selector.MatchLabels, labels)
		}
		var s serviceSpec
		decodeSpec(t, *svc, &s)
		if !selects(s.Selector, labels) {
			t.Errorf("Service/%s: selector %v does not select the pods %v", name, s.Selector, labels)
		}

		pod := w.Template.Spec
		if sc := pod.SecurityContext; sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || sc.SeccompProfile.Type != "RuntimeDefault" {
			t.Errorf("Deployment/%s: the pod must set runAsNonRoot: true and seccompProfile RuntimeDefault", name)
		}
		for _, c := range slices.Concat(pod.InitContainers, pod.Containers) {
			sc := c.SecurityContext
			if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation || !slices.Contains(sc.Capabilities.Drop, "ALL") {
				t.Errorf("Deployment/%s container %s: must set allowPrivilegeEscalation: false and drop ALL capabilities", name, c.Name)
			}
		}
		c := containerOf(t, *dep)
		if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet.Path != "/readyz" {
			t.Errorf("Deployment/%s: readiness probe must be GET /readyz", name)
		}
		if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet.Path != "/healthz" {
			t.Errorf("Deployment/%s: liveness probe must be GET /healthz", name)
		}
	}

	checkSecretRefs(t, objs)
	checkSecretValues(t, objs, vals)
}

// TestChartReleaseName: the addon only registers under the name acquire, so any
// other release name fails the render. The operator reports a template failure
// by its location alone, so that location must name the reason.
func TestChartReleaseName(t *testing.T) {
	_, err := helmTemplateOutput(t, "acquire-test", testValues)
	if err == nil {
		t.Fatal("rendered under the release name acquire-test")
	}
	// The operator's reduction of a template error to its location.
	loc := regexp.MustCompile(`\(([^()]+?\.(?:yaml|yml|tpl|txt):\d+(?::\d+)?)\)`).FindStringSubmatch(err.Error())
	if loc == nil || !strings.Contains(loc[1], "templates/release-name-must-be-acquire.yaml:") {
		t.Errorf("want a template error located in templates/release-name-must-be-acquire.yaml, got %v", err)
	}

	out, err := helmTemplateOutput(t, release, testValues)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "release-name-must-be-acquire") {
		t.Error("the release name guard renders output under the name acquire")
	}
}

// TestChartInputs: each install input reaches the variable the service reads
// it as; secret inputs through the chart's Secret.
func TestChartInputs(t *testing.T) {
	objs := mustRender(t)
	vals := values(t)
	acquire := containerOf(t, mustFind(t, objs, "Deployment", "acquire"))
	gateway := containerOf(t, mustFind(t, objs, "Deployment", "download-gateway"))
	stored := secretData(t, objs)

	for env, path := range map[string]string{
		"PG_URL":                      "database.url",
		"ACQUIRE_SVC_CLIENT_SECRET":   "oidc.serviceClientSecret",
		"TMDB_API_KEY":                "tmdb.apiKey",
		"ACQUIRE_CONFIG_KEY":          "config.key",
		"ACQUIRE_CONFIG_KEY_PREVIOUS": "config.previousKey",
	} {
		e := envOf(acquire, env)
		if e == nil || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Errorf("acquire: %s must come from a secretKeyRef", env)
			continue
		}
		ref := e.ValueFrom.SecretKeyRef
		if stored[ref.Name][ref.Key] != str(t, vals, path) {
			t.Errorf("acquire: %s (Secret %s key %s) does not hold the value of %s", env, ref.Name, ref.Key, path)
		}
	}
	wantEnv(t, "acquire", acquire, map[string]string{
		"ACQUIRE_OIDC_CLIENT_ID":          "laedeli-acquire",
		"ACQUIRE_SVC_CLIENT_ID":           "laedeli-acquire-svc",
		"ACQUIRE_ADMIN_ROLE":              "zaentrum-admin",
		"ACQUIRE_USER_ROLE":               "zaentrum-user",
		"ACQUIRE_ENDPOINT_DENY":           str(t, vals, "endpoints.deny"),
		"ACQUIRE_ENDPOINT_ALLOW_INTERNAL": "false",
		"ACQUIRE_DOWNLOADS_ROOT":          "/var/lib/katalog",
		"DOWNLOAD_GATEWAY_URL":            "http://download-gateway",
		"KATALOG_URL":                     "http://katalog-api",
		"KATALOG_MANAGER_URL":             "http://katalog-manager-api",
	})
	// The gateway accepts acquire's service client, and only it.
	wantEnv(t, "download-gateway", gateway, map[string]string{"ALLOWED_CLIENTS": "laedeli-acquire-svc"})

	if want := "ghcr.io/laedeli/acquire:" + chartMeta(t).AppVersion; acquire.Image != want {
		t.Errorf("acquire: image %s, want %s (the tag defaults to the chart's appVersion)", acquire.Image, want)
	}
}

// TestChartPlatformFacts: everything platform-specific comes from the zaentrum
// block: issuer, split-horizon alias, brokers and their client certificate,
// consumer group, media claim, pull secrets and the part-of label.
func TestChartPlatformFacts(t *testing.T) {
	objs := mustRender(t)
	vals := values(t)
	issuer := str(t, vals, "zaentrum.issuer")
	brokers := str(t, vals, "zaentrum.events.brokers")
	prefix := str(t, vals, "zaentrum.events.topicPrefix")
	tlsSecret := str(t, vals, "zaentrum.events.tlsSecret")

	acquireDep := mustFind(t, objs, "Deployment", "acquire")
	gatewayDep := mustFind(t, objs, "Deployment", "download-gateway")

	wantEnv(t, "acquire", containerOf(t, acquireDep), map[string]string{
		"OIDC_ISSUER":        issuer,
		"OIDC_TOKEN_URL":     issuer + "/protocol/openid-connect/token",
		"KAFKA_BROKERS":      brokers,
		"KAFKA_TOPIC_PREFIX": prefix,
		"KAFKA_GROUP_ID":     "zaentrum-example-acquire",
		"KAFKA_CERT_DIR":     "/etc/kafka-cert",
	})
	wantMount(t, acquireDep, "/etc/kafka-cert", "secret", tlsSecret)
	wantMount(t, acquireDep, "/var/lib/katalog", "persistentVolumeClaim", str(t, vals, "zaentrum.media.claimName"))

	wantEnv(t, "download-gateway", containerOf(t, gatewayDep), map[string]string{
		"OIDC_ISSUER":        issuer,
		"KAFKA_BROKERS":      brokers,
		"KAFKA_TOPIC_PREFIX": prefix,
		"KAFKA_TLS_CERT":     "/etc/kafka-cert/user.crt",
		"KAFKA_TLS_KEY":      "/etc/kafka-cert/user.key",
		"KAFKA_TLS_CA":       "/etc/kafka-cert/ca.crt",
	})
	wantMount(t, gatewayDep, "/etc/kafka-cert", "secret", tlsSecret)

	u, err := url.Parse(issuer)
	if err != nil {
		t.Fatal(err)
	}
	pullSecrets, _ := lookup(vals, "zaentrum.imagePullSecrets")
	for _, dep := range []object{acquireDep, gatewayDep} {
		pod := workloadOf(t, dep).Template.Spec
		alias := str(t, vals, "zaentrum.issuerHostAliasIP")
		if len(pod.HostAliases) != 1 || pod.HostAliases[0].IP != alias || !slices.Equal(pod.HostAliases[0].Hostnames, []string{u.Hostname()}) {
			t.Errorf("%s: hostAliases %+v, want %s → %s", dep.ref(), pod.HostAliases, u.Hostname(), alias)
		}
		var names []any
		for _, s := range pod.ImagePullSecrets {
			names = append(names, s.Name)
		}
		if !slices.Equal(names, pullSecrets.([]any)) {
			t.Errorf("%s: imagePullSecrets %v, want %v", dep.ref(), names, pullSecrets)
		}
	}

	partOf := str(t, vals, "zaentrum.partOf")
	for _, o := range objs {
		if got := o.Metadata.Labels["app.kubernetes.io/part-of"]; got != partOf {
			t.Errorf("%s: part-of label %q, want %q", o.ref(), got, partOf)
		}
	}
}

// TestChartPlaintextAndOptionalFacts: on plaintext brokers (no events TLS
// secret) the render is still valid and uses no certificate: acquire gets an
// empty KAFKA_CERT_DIR, the gateway no TLS files, and nothing is mounted. No
// split horizon, pull secrets, media claim or optional secret inputs render
// nothing for them either.
func TestChartPlaintextAndOptionalFacts(t *testing.T) {
	objs := mustRender(t,
		"--set", "zaentrum.events.tlsSecret=",
		"--set", "zaentrum.issuerHostAliasIP=",
		"--set", "zaentrum.imagePullSecrets=null",
		"--set", "zaentrum.media.claimName=",
		"--set", "zaentrum.partOf=",
		"--set", "tmdb.apiKey=",
		"--set", "config.previousKey=",
	)
	guard := platformGuard(t, values(t), chartMeta(t))
	guard.MediaClaim, guard.EventsTLSSecret, guard.PullSecrets = "", "", nil
	if v := violations(objs, guard); len(v) > 0 {
		t.Errorf("the operator would refuse the plaintext render:\n  %s", strings.Join(v, "\n  "))
	}

	wantEnv(t, "acquire", containerOf(t, mustFind(t, objs, "Deployment", "acquire")), map[string]string{"KAFKA_CERT_DIR": ""})
	for _, name := range []string{"acquire", "download-gateway"} {
		dep := mustFind(t, objs, "Deployment", name)
		pod := workloadOf(t, dep).Template.Spec
		c := containerOf(t, dep)
		if len(pod.Volumes) > 0 || len(c.VolumeMounts) > 0 {
			t.Errorf("%s: want no volumes or mounts, got %v and %v", dep.ref(), pod.Volumes, c.VolumeMounts)
		}
		if len(pod.HostAliases) > 0 || len(pod.ImagePullSecrets) > 0 {
			t.Errorf("%s: want no hostAliases or pull secrets, got %v and %v", dep.ref(), pod.HostAliases, pod.ImagePullSecrets)
		}
		for _, e := range c.Env {
			if strings.HasPrefix(e.Name, "KAFKA_TLS_") || (e.Name == "KAFKA_CERT_DIR" && e.Value.Value != "") {
				t.Errorf("%s: %s=%q set for plaintext brokers", dep.ref(), e.Name, e.Value.Value)
			}
		}
		if _, ok := dep.Metadata.Labels["app.kubernetes.io/part-of"]; ok {
			t.Errorf("%s: part-of label without a zaentrum.partOf", dep.ref())
		}
	}
	if strings.Contains(strings.Join(rawOf(objs), "\n"), "kafka-cert") {
		t.Error("the plaintext render still names the kafka-cert volume")
	}
	for key := range secretData(t, objs)["acquire-secrets"] {
		if key == "tmdb-api-key" || key == "config-previous-key" {
			t.Errorf("Secret/acquire-secrets: %s rendered for an input that was not given", key)
		}
	}
	checkSecretRefs(t, objs)
}

// TestChartRequiresInputs: an install without a required secret input is
// refused by the schema, naming the input. The operator reports that as
// plan.valuesErrors.
func TestChartRequiresInputs(t *testing.T) {
	vals := values(t)
	for _, path := range []string{"database.url", "oidc.serviceClientSecret", "config.key"} {
		t.Run(path, func(t *testing.T) {
			b, err := yaml.Marshal(without(vals, path))
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(file, b, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = helmTemplateOutput(t, release, file)
			leaf := path[strings.LastIndex(path, ".")+1:]
			if err == nil || !strings.Contains(err.Error(), "schema") || !strings.Contains(err.Error(), leaf) {
				t.Errorf("rendering without %s: want a schema error naming %s, got %v", path, leaf, err)
			}
		})
	}
}

// ── manifests ───────────────────────────────────────────────────────────────

// object is one rendered manifest, decoded as far as the checks need.
type object struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name            string            `yaml:"name"`
		Namespace       string            `yaml:"namespace"`
		Labels          map[string]string `yaml:"labels"`
		Annotations     map[string]string `yaml:"annotations"`
		OwnerReferences []any             `yaml:"ownerReferences"`
	} `yaml:"metadata"`
	Type       string            `yaml:"type"`
	Spec       yaml.Node         `yaml:"spec"`
	Data       map[string]string `yaml:"data"`
	StringData map[string]string `yaml:"stringData"`

	raw string
}

func (o object) ref() string { return o.Kind + "/" + o.Metadata.Name }

type workloadSpec struct {
	Selector struct {
		MatchLabels map[string]string `yaml:"matchLabels"`
	} `yaml:"selector"`
	Template struct {
		Metadata struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
		Spec podSpec `yaml:"spec"`
	} `yaml:"template"`
}

type podSpec struct {
	HostNetwork        bool              `yaml:"hostNetwork"`
	HostPID            bool              `yaml:"hostPID"`
	HostIPC            bool              `yaml:"hostIPC"`
	PriorityClassName  string            `yaml:"priorityClassName"`
	NodeName           string            `yaml:"nodeName"`
	NodeSelector       map[string]string `yaml:"nodeSelector"`
	ServiceAccountName string            `yaml:"serviceAccountName"`
	ServiceAccount     string            `yaml:"serviceAccount"` // deprecated alias
	Tolerations        []struct {
		Key      string `yaml:"key"`
		Operator string `yaml:"operator"`
	} `yaml:"tolerations"`
	Affinity struct {
		NodeAffinity *struct {
			Required struct {
				NodeSelectorTerms []nodeSelectorTerm `yaml:"nodeSelectorTerms"`
			} `yaml:"requiredDuringSchedulingIgnoredDuringExecution"`
			Preferred []struct {
				Preference nodeSelectorTerm `yaml:"preference"`
			} `yaml:"preferredDuringSchedulingIgnoredDuringExecution"`
		} `yaml:"nodeAffinity"`
	} `yaml:"affinity"`
	SecurityContext struct {
		RunAsNonRoot   *bool  `yaml:"runAsNonRoot"`
		RunAsUser      *int64 `yaml:"runAsUser"`
		SeccompProfile struct {
			Type string `yaml:"type"`
		} `yaml:"seccompProfile"`
		SELinuxOptions struct {
			Type string `yaml:"type"`
		} `yaml:"seLinuxOptions"`
		Sysctls         []any `yaml:"sysctls"`
		AppArmorProfile struct {
			Type string `yaml:"type"`
		} `yaml:"appArmorProfile"`
		WindowsOptions struct {
			HostProcess *bool `yaml:"hostProcess"`
		} `yaml:"windowsOptions"`
	} `yaml:"securityContext"`
	HostAliases []struct {
		IP        string   `yaml:"ip"`
		Hostnames []string `yaml:"hostnames"`
	} `yaml:"hostAliases"`
	ImagePullSecrets []struct {
		Name string `yaml:"name"`
	} `yaml:"imagePullSecrets"`
	InitContainers      []container      `yaml:"initContainers"`
	Containers          []container      `yaml:"containers"`
	EphemeralContainers []container      `yaml:"ephemeralContainers"`
	Volumes             []map[string]any `yaml:"volumes"`
}

type nodeSelectorTerm struct {
	MatchExpressions []struct {
		Key string `yaml:"key"`
	} `yaml:"matchExpressions"`
	MatchFields []struct {
		Key string `yaml:"key"`
	} `yaml:"matchFields"`
}

type container struct {
	Name  string `yaml:"name"`
	Image string `yaml:"image"`
	Ports []struct {
		Name          string `yaml:"name"`
		ContainerPort int    `yaml:"containerPort"`
		HostPort      int    `yaml:"hostPort"`
	} `yaml:"ports"`
	Env     []envVar `yaml:"env"`
	EnvFrom []struct {
		SecretRef *struct {
			Name string `yaml:"name"`
		} `yaml:"secretRef"`
		ConfigMapRef *struct {
			Name string `yaml:"name"`
		} `yaml:"configMapRef"`
	} `yaml:"envFrom"`
	VolumeMounts []struct {
		Name      string `yaml:"name"`
		MountPath string `yaml:"mountPath"`
		ReadOnly  bool   `yaml:"readOnly"`
	} `yaml:"volumeMounts"`
	SecurityContext struct {
		Privileged               *bool  `yaml:"privileged"`
		AllowPrivilegeEscalation *bool  `yaml:"allowPrivilegeEscalation"`
		RunAsUser                *int64 `yaml:"runAsUser"`
		RunAsNonRoot             *bool  `yaml:"runAsNonRoot"`
		ProcMount                string `yaml:"procMount"`
		SELinuxOptions           struct {
			Type string `yaml:"type"`
		} `yaml:"seLinuxOptions"`
		AppArmorProfile struct {
			Type string `yaml:"type"`
		} `yaml:"appArmorProfile"`
		WindowsOptions struct {
			HostProcess *bool `yaml:"hostProcess"`
		} `yaml:"windowsOptions"`
		Capabilities struct {
			Add  []string `yaml:"add"`
			Drop []string `yaml:"drop"`
		} `yaml:"capabilities"`
	} `yaml:"securityContext"`
	ReadinessProbe *probe `yaml:"readinessProbe"`
	LivenessProbe  *probe `yaml:"livenessProbe"`
}

type envVar struct {
	Name      string    `yaml:"name"`
	Value     yaml.Node `yaml:"value"` // Kind 0 when unset
	ValueFrom *struct {
		SecretKeyRef    *keyRef `yaml:"secretKeyRef"`
		ConfigMapKeyRef *keyRef `yaml:"configMapKeyRef"`
	} `yaml:"valueFrom"`
}

type keyRef struct {
	Name     string `yaml:"name"`
	Key      string `yaml:"key"`
	Optional bool   `yaml:"optional"`
}

type probe struct {
	HTTPGet struct {
		Path string `yaml:"path"`
	} `yaml:"httpGet"`
}

type serviceSpec struct {
	Type                     string            `yaml:"type"`
	Selector                 map[string]string `yaml:"selector"`
	Ports                    []servicePort     `yaml:"ports"`
	ExternalIPs              []string          `yaml:"externalIPs"`
	LoadBalancerSourceRanges []string          `yaml:"loadBalancerSourceRanges"`
	ExternalName             string            `yaml:"externalName"`
	LoadBalancerIP           string            `yaml:"loadBalancerIP"`
}

type servicePort struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

type pvcSpec struct {
	VolumeName    string         `yaml:"volumeName"`
	DataSource    map[string]any `yaml:"dataSource"`
	DataSourceRef map[string]any `yaml:"dataSourceRef"`
}

var docSeparator = regexp.MustCompile(`(?m)^---[ \t]*$`)

// decodeManifests splits a YAML stream into objects, skipping documents that
// hold only comments.
func decodeManifests(stream string) ([]object, error) {
	var objs []object
	for _, doc := range docSeparator.Split(stream, -1) {
		var content any
		if err := yaml.Unmarshal([]byte(doc), &content); err != nil {
			return nil, fmt.Errorf("a document does not parse: %v\n%s", err, doc)
		}
		if content == nil {
			continue
		}
		var o object
		if err := yaml.Unmarshal([]byte(doc), &o); err != nil {
			return nil, fmt.Errorf("a document does not decode: %v\n%s", err, doc)
		}
		o.raw = doc
		objs = append(objs, o)
	}
	return objs, nil
}

// helmTemplateOutput renders the chart under a release name with one values
// file and extra arguments, and returns what helm printed.
func helmTemplateOutput(t *testing.T, releaseName, valuesFile string, args ...string) (string, error) {
	t.Helper()
	helm := os.Getenv("HELM")
	if helm == "" {
		var err error
		if helm, err = exec.LookPath("helm"); err != nil {
			if os.Getenv("REQUIRE_HELM") != "" {
				t.Fatal("helm not found (set HELM or put helm on PATH); REQUIRE_HELM is set")
			}
			t.Skip("helm not found: set HELM or put helm on PATH")
		}
	}
	cmd := exec.Command(helm, append([]string{"template", releaseName, chartDir,
		"--namespace", namespace, "--values", valuesFile}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("helm template: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// mustRender renders the chart as the addon acquire with the CI values and
// extra arguments.
func mustRender(t *testing.T, args ...string) []object {
	t.Helper()
	out, err := helmTemplateOutput(t, release, testValues, args...)
	if err != nil {
		t.Fatal(err)
	}
	objs, err := decodeManifests(out)
	if err != nil {
		t.Fatal(err)
	}
	return objs
}

// platformGuard is the guard input the operator would use for these values.
func platformGuard(t *testing.T, vals map[string]any, meta chartYAML) guardInput {
	t.Helper()
	var pull []string
	if list, ok := lookup(vals, "zaentrum.imagePullSecrets"); ok {
		for _, s := range list.([]any) {
			pull = append(pull, s.(string))
		}
	}
	return guardInput{
		Namespace:       namespace,
		Addon:           release,
		MediaClaim:      str(t, vals, "zaentrum.media.claimName"),
		Primary:         meta.Annotations["zaentrum.io/primary"],
		EventsTLSSecret: str(t, vals, "zaentrum.events.tlsSecret"),
		PullSecrets:     pull,
	}
}

// ── chart checks beyond the guardrails ──────────────────────────────────────

// checkSecretRefs: env values are strings, and every env secretKeyRef names a
// Secret the chart renders and a key it holds (unless the reference is
// optional), so no value has to come from outside the chart.
func checkSecretRefs(t *testing.T, objs []object) {
	t.Helper()
	stored := secretData(t, objs)
	for _, o := range objs {
		if o.Kind != "Deployment" && o.Kind != "Job" {
			continue
		}
		pod := workloadOf(t, o).Template.Spec
		for _, c := range slices.Concat(pod.InitContainers, pod.Containers) {
			for _, e := range c.Env {
				if e.Value.Kind != 0 && e.Value.Tag != "!!str" {
					t.Errorf("%s container %s: env %s is %s, must be a string", o.ref(), c.Name, e.Name, e.Value.Tag)
				}
				if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
					continue
				}
				ref := e.ValueFrom.SecretKeyRef
				keys, ok := stored[ref.Name]
				if !ok {
					t.Errorf("%s container %s: env %s reads Secret %q, which the chart does not render", o.ref(), c.Name, e.Name, ref.Name)
					continue
				}
				if _, ok := keys[ref.Key]; !ok && !ref.Optional {
					t.Errorf("%s container %s: env %s reads key %q, which Secret %s does not hold", o.ref(), c.Name, e.Name, ref.Key, ref.Name)
				}
			}
		}
	}
}

// checkSecretValues: every writeOnly input of values.schema.json is rendered
// into a Secret, and neither its value nor its base64 form appears in any
// other object.
func checkSecretValues(t *testing.T, objs []object, vals map[string]any) {
	t.Helper()
	inputs := secretInputs(t)
	if len(inputs) == 0 {
		t.Fatal("values.schema.json declares no writeOnly inputs")
	}
	stored := secretData(t, objs)
	for _, path := range inputs {
		secret := str(t, vals, path)
		found := false
		for _, keys := range stored {
			for _, v := range keys {
				found = found || v == secret
			}
		}
		if !found {
			t.Errorf("secret input %s is not rendered into a Secret", path)
		}
		for _, o := range objs {
			if o.Kind == "Secret" {
				continue
			}
			if strings.Contains(o.raw, secret) || strings.Contains(o.raw, base64.StdEncoding.EncodeToString([]byte(secret))) {
				t.Errorf("%s: contains the value of secret input %s", o.ref(), path)
			}
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func find(objs []object, kind, name string) *object {
	for i := range objs {
		if objs[i].Kind == kind && objs[i].Metadata.Name == name {
			return &objs[i]
		}
	}
	return nil
}

func mustFind(t *testing.T, objs []object, kind, name string) object {
	t.Helper()
	o := find(objs, kind, name)
	if o == nil {
		t.Fatalf("%s/%s is not rendered; rendered %v", kind, name, refs(objs))
	}
	return *o
}

func refs(objs []object) []string {
	var out []string
	for _, o := range objs {
		out = append(out, o.ref())
	}
	return out
}

func rawOf(objs []object) []string {
	var out []string
	for _, o := range objs {
		out = append(out, o.raw)
	}
	return out
}

func decodeSpec(t *testing.T, o object, into any) {
	t.Helper()
	if err := o.Spec.Decode(into); err != nil {
		t.Fatalf("%s: spec: %v", o.ref(), err)
	}
}

func workloadOf(t *testing.T, o object) workloadSpec {
	t.Helper()
	var w workloadSpec
	decodeSpec(t, o, &w)
	return w
}

// containerOf returns a Deployment's one container.
func containerOf(t *testing.T, o object) container {
	t.Helper()
	cs := workloadOf(t, o).Template.Spec.Containers
	if len(cs) != 1 {
		t.Fatalf("%s: want one container, got %d", o.ref(), len(cs))
	}
	return cs[0]
}

func envOf(c container, name string) *envVar {
	for i := range c.Env {
		if c.Env[i].Name == name {
			return &c.Env[i]
		}
	}
	return nil
}

// wantEnv: each variable is set to a plain value.
func wantEnv(t *testing.T, component string, c container, want map[string]string) {
	t.Helper()
	for name, value := range want {
		e := envOf(c, name)
		switch {
		case e == nil:
			t.Errorf("%s: %s is not set", component, name)
		case e.Value.Kind == 0:
			t.Errorf("%s: %s has no plain value", component, name)
		case e.Value.Value != value:
			t.Errorf("%s: %s = %q, want %q", component, name, e.Value.Value, value)
		}
	}
}

// wantMount: the Deployment's container mounts path read-only from a volume of
// that type whose secretName or claimName is source.
func wantMount(t *testing.T, dep object, path, volType, source string) {
	t.Helper()
	w := workloadOf(t, dep)
	field := map[string]string{"secret": "secretName", "persistentVolumeClaim": "claimName"}[volType]
	for _, m := range containerOf(t, dep).VolumeMounts {
		if m.MountPath != path {
			continue
		}
		if !m.ReadOnly {
			t.Errorf("%s: %s is mounted writable", dep.ref(), path)
		}
		for _, v := range w.Template.Spec.Volumes {
			if v["name"] != m.Name {
				continue
			}
			src, _ := v[volType].(map[string]any)
			if src == nil || src[field] != source {
				t.Errorf("%s: %s is mounted from %v, want %s %s", dep.ref(), path, v, volType, source)
			}
			return
		}
		t.Errorf("%s: the mount at %s names no volume", dep.ref(), path)
		return
	}
	t.Errorf("%s: nothing is mounted at %s", dep.ref(), path)
}

func selects(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// secretData decodes every rendered Secret: name → key → value.
func secretData(t *testing.T, objs []object) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	for _, o := range objs {
		if o.Kind != "Secret" {
			continue
		}
		keys := map[string]string{}
		for k, v := range o.Data {
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				t.Errorf("%s: data.%s is not base64", o.ref(), k)
				continue
			}
			keys[k] = string(b)
		}
		for k, v := range o.StringData {
			keys[k] = v
		}
		out[o.Metadata.Name] = keys
	}
	return out
}

// secretInputs lists the dotted paths values.schema.json marks writeOnly.
func secretInputs(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(chartDir, "values.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	type property struct {
		WriteOnly  bool                `json:"writeOnly"`
		Properties map[string]property `json:"properties"`
	}
	var root property
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("values.schema.json: %v", err)
	}
	var paths []string
	var walk func(prefix string, p property)
	walk = func(prefix string, p property) {
		for name, child := range p.Properties {
			path := strings.TrimPrefix(prefix+"."+name, ".")
			if child.WriteOnly {
				paths = append(paths, path)
			}
			walk(path, child)
		}
	}
	walk("", root)
	sort.Strings(paths)
	return paths
}

type chartYAML struct {
	AppVersion  string            `yaml:"appVersion"`
	Annotations map[string]string `yaml:"annotations"`
}

func chartMeta(t *testing.T) chartYAML {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var c chartYAML
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatalf("Chart.yaml: %v", err)
	}
	return c
}

// values reads the CI values as a tree for dotted-path lookups.
func values(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(testValues)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", testValues, err)
	}
	return v
}

func lookup(tree map[string]any, path string) (any, bool) {
	var cur any = tree
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// str is a non-empty string the CI values must set.
func str(t *testing.T, tree map[string]any, path string) string {
	t.Helper()
	v, _ := lookup(tree, path)
	s, _ := v.(string)
	if s == "" {
		t.Fatalf("%s must set %s", testValues, path)
	}
	return s
}

// without copies tree leaving out the dotted path.
func without(tree map[string]any, path string) map[string]any {
	head, rest, nested := strings.Cut(path, ".")
	out := map[string]any{}
	for k, v := range tree {
		switch m, isMap := v.(map[string]any); {
		case k != head:
			out[k] = v
		case nested && isMap:
			out[k] = without(m, rest)
		case nested:
			out[k] = v
		}
	}
	return out
}
