package charts

// A copy of the zaentrum operator's addon guardrails (its addon Violations), so
// the chart is held to them here before the operator ever plans it. The copy
// is kept honest by TestGuardrailCopy, which feeds it one offending manifest
// per rule. One deliberate difference: every name with the addon's values
// prefix is reserved, not just the values objects of one install.

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// guardInput is what rendered objects are checked against (the operator's
// GuardInput).
type guardInput struct {
	Namespace string
	// Addon is the addon's name; zaentrum-addon-<Addon>-… names are its values objects.
	Addon string
	// MediaClaim is zaentrum.media.claimName, which pods may mount unrendered.
	MediaClaim string
	// Primary is the zaentrum.io/primary Service; "" skips that check.
	Primary string
	// EventsTLSSecret is zaentrum.events.tlsSecret, which pods may reference unrendered.
	EventsTLSSecret string
	// PullSecrets is zaentrum.imagePullSecrets, which pods may use and reference unrendered.
	PullSecrets []string
}

var (
	// allowedAPIVersions: the only kinds a chart may render, each with its one apiVersion.
	allowedAPIVersions = map[string]string{
		"ServiceAccount":        "v1",
		"Secret":                "v1",
		"ConfigMap":             "v1",
		"PersistentVolumeClaim": "v1",
		"Service":               "v1",
		"Deployment":            "apps/v1",
		"Job":                   "batch/v1",
	}
	allowedVolumes = []string{"configMap", "secret", "emptyDir", "projected", "downwardAPI", "persistentVolumeClaim"}
	// allowedSecretTypes leaves out kubernetes.io/service-account-token, which
	// the token controller would fill with a real token.
	allowedSecretTypes = []string{"", "Opaque", "kubernetes.io/tls", "kubernetes.io/dockerconfigjson", "kubernetes.io/basic-auth", "kubernetes.io/ssh-auth"}
	saAnnotations      = []string{"kubernetes.io/service-account.name", "kubernetes.io/service-account.uid"}
	controlPlaneRoles  = []string{"node-role.kubernetes.io/control-plane", "node-role.kubernetes.io/master"}
)

// violations applies the guardrails, one line per refusal in the shape of the
// operator's plan.violations.
func violations(objs []object, in guardInput) []string {
	rendered := map[string]bool{}
	for _, o := range objs {
		rendered[o.ref()] = true
	}
	refs := podRefs{
		rendered:     rendered,
		mediaClaim:   in.MediaClaim,
		mountSecrets: map[string]bool{},
		pullSecrets:  map[string]bool{},
	}
	for _, o := range objs {
		if o.Kind == "Secret" {
			refs.mountSecrets[o.Metadata.Name] = true
			refs.pullSecrets[o.Metadata.Name] = true
		}
	}
	if in.EventsTLSSecret != "" {
		refs.mountSecrets[in.EventsTLSSecret] = true
	}
	for _, s := range in.PullSecrets {
		refs.mountSecrets[s] = true
		refs.pullSecrets[s] = true
	}

	var out []string
	seen := map[string]bool{}
	for _, o := range objs {
		refuse := func(format string, args ...any) {
			out = append(out, o.ref()+": "+fmt.Sprintf(format, args...))
		}
		if o.Kind == "" || o.Metadata.Name == "" {
			refuse("object without kind or metadata.name")
			continue
		}
		version, ok := allowedAPIVersions[o.Kind]
		if !ok {
			refuse("kind not allowed")
			continue
		}
		if o.APIVersion != version {
			refuse("apiVersion %s not allowed (use %s)", o.APIVersion, version)
			continue
		}
		if seen[o.ref()] {
			refuse("rendered more than once")
		}
		seen[o.ref()] = true
		if ns := o.Metadata.Namespace; ns != "" && ns != in.Namespace {
			refuse("namespace %s not allowed (addons install into %s)", ns, in.Namespace)
		}
		if strings.HasPrefix(o.Metadata.Name, "zaentrum-addon-"+in.Addon+"-") {
			refuse("name reserved for the addon's values")
		}
		if len(o.Metadata.OwnerReferences) > 0 {
			refuse("metadata.ownerReferences not allowed (the operator sets the owner)")
		}
		for _, key := range saAnnotations {
			if _, ok := o.Metadata.Annotations[key]; ok {
				refuse("annotation %s not allowed", key)
			}
		}
		switch o.Kind {
		case "Secret":
			if !slices.Contains(allowedSecretTypes, o.Type) {
				refuse("Secret type %s not allowed", o.Type)
			}
		case "Service":
			var s serviceSpec
			if err := o.Spec.Decode(&s); err != nil {
				refuse("invalid spec: %v", err)
				continue
			}
			if s.Type != "" && s.Type != "ClusterIP" {
				refuse("Service type %s not allowed (ClusterIP only)", s.Type)
			}
			if len(s.ExternalIPs) > 0 {
				refuse("Service spec.externalIPs not allowed")
			}
			if len(s.LoadBalancerSourceRanges) > 0 {
				refuse("Service spec.loadBalancerSourceRanges not allowed")
			}
			if s.ExternalName != "" {
				refuse("Service spec.externalName not allowed")
			}
			if s.LoadBalancerIP != "" {
				refuse("Service spec.loadBalancerIP not allowed")
			}
		case "PersistentVolumeClaim":
			var p pvcSpec
			if err := o.Spec.Decode(&p); err != nil {
				refuse("invalid spec: %v", err)
				continue
			}
			if p.VolumeName != "" {
				refuse("PVC spec.volumeName not allowed (binds a specific PV)")
			}
			if len(p.DataSource) > 0 {
				refuse("PVC spec.dataSource not allowed")
			}
			if len(p.DataSourceRef) > 0 {
				refuse("PVC spec.dataSourceRef not allowed")
			}
		case "Deployment", "Job":
			out = append(out, podViolations(o, refs)...)
		}
	}
	return append(out, primaryViolations(objs, in.Primary)...)
}

// podRefs are the names a pod may reference.
type podRefs struct {
	rendered     map[string]bool // "Kind/name" the chart renders
	mediaClaim   string
	mountSecrets map[string]bool // rendered Secrets, the events TLS secret, the pull secrets
	pullSecrets  map[string]bool // rendered Secrets and the pull secrets
}

func podViolations(o object, refs podRefs) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, o.ref()+": "+fmt.Sprintf(format, args...))
	}
	var w workloadSpec
	if err := o.Spec.Decode(&w); err != nil {
		add("invalid pod template: %v", err)
		return out
	}
	spec := w.Template.Spec

	if spec.HostNetwork {
		add("hostNetwork not allowed")
	}
	if spec.HostPID {
		add("hostPID not allowed")
	}
	if spec.HostIPC {
		add("hostIPC not allowed")
	}
	if spec.PriorityClassName != "" {
		add("priorityClassName not allowed")
	}
	if spec.NodeName != "" {
		add("nodeName not allowed")
	}
	for key := range spec.NodeSelector {
		if slices.Contains(controlPlaneRoles, key) {
			add("nodeSelector %s not allowed (control-plane placement)", key)
		}
	}
	for _, tol := range spec.Tolerations {
		if slices.Contains(controlPlaneRoles, tol.Key) || (tol.Key == "" && tol.Operator == "Exists") {
			add("toleration for control-plane taints not allowed")
		}
	}
	if na := spec.Affinity.NodeAffinity; na != nil {
		terms := slices.Clone(na.Required.NodeSelectorTerms)
		for _, p := range na.Preferred {
			terms = append(terms, p.Preference)
		}
	affinity:
		for _, term := range terms {
			for _, req := range slices.Concat(term.MatchExpressions, term.MatchFields) {
				if slices.Contains(controlPlaneRoles, req.Key) {
					add("nodeAffinity for control-plane nodes not allowed")
					break affinity
				}
			}
		}
	}

	sc := spec.SecurityContext
	if sc.SELinuxOptions.Type != "" {
		add("seLinuxOptions.type not allowed")
	}
	if len(sc.Sysctls) > 0 {
		add("sysctls not allowed")
	}
	if sc.AppArmorProfile.Type == "Unconfined" {
		add("appArmorProfile Unconfined not allowed")
	}
	if sc.WindowsOptions.HostProcess != nil && *sc.WindowsOptions.HostProcess {
		add("windowsOptions.hostProcess not allowed")
	}
	if sc.RunAsUser != nil && *sc.RunAsUser == 0 {
		add("runAsUser 0 not allowed")
	}
	if sc.RunAsNonRoot != nil && !*sc.RunAsNonRoot {
		add("runAsNonRoot false not allowed")
	}

	for _, sa := range []string{spec.ServiceAccountName, spec.ServiceAccount} {
		if sa != "" && sa != "default" && !refs.rendered["ServiceAccount/"+sa] {
			add("serviceAccountName %s not allowed (not rendered by the chart)", sa)
			break
		}
	}
	for _, s := range spec.ImagePullSecrets {
		if !refs.pullSecrets[s.Name] {
			add("imagePullSecrets %s not allowed (not rendered by the chart or a platform pull secret)", s.Name)
		}
	}
	for _, v := range spec.Volumes {
		out = append(out, volumeViolations(o, v, refs)...)
	}
	for _, c := range slices.Concat(spec.InitContainers, spec.Containers, spec.EphemeralContainers) {
		out = append(out, containerViolations(o, c, refs)...)
	}
	return out
}

// volumeViolations checks one volume's source and the objects it names. A
// volume without a source is an emptyDir, as the API server defaults it.
func volumeViolations(o object, v map[string]any, refs podRefs) []string {
	add := func(format string, args ...any) string {
		return o.ref() + ": " + fmt.Sprintf(format, args...)
	}
	name, _ := v["name"].(string)
	var sources []string
	for key := range v {
		if key != "name" {
			sources = append(sources, key)
		}
	}
	sort.Strings(sources)
	source := strings.Join(sources, "+")
	if source == "" {
		source = "emptyDir"
	}
	field := func(key, sub string) string {
		m, _ := v[key].(map[string]any)
		s, _ := m[sub].(string)
		return s
	}
	switch source {
	case "persistentVolumeClaim":
		if claim := field(source, "claimName"); claim != refs.mediaClaim && !refs.rendered["PersistentVolumeClaim/"+claim] {
			return []string{add("persistentVolumeClaim %s not allowed (not rendered by the chart)", claim)}
		}
	case "secret":
		if secret := field(source, "secretName"); !refs.mountSecrets[secret] {
			return []string{add("secret volume %s references %s, not rendered by the chart", name, secret)}
		}
	case "configMap":
		if cm := field(source, "name"); !refs.rendered["ConfigMap/"+cm] {
			return []string{add("configMap volume %s references %s, not rendered by the chart", name, cm)}
		}
	case "projected":
		var out []string
		m, _ := v[source].(map[string]any)
		list, _ := m["sources"].([]any)
		for _, item := range list {
			s, _ := item.(map[string]any)
			if secret, ok := s["secret"].(map[string]any); ok {
				if n, _ := secret["name"].(string); !refs.mountSecrets[n] {
					out = append(out, add("projected volume %s references secret %s, not rendered by the chart", name, n))
				}
			}
			if cm, ok := s["configMap"].(map[string]any); ok {
				if n, _ := cm["name"].(string); !refs.rendered["ConfigMap/"+n] {
					out = append(out, add("projected volume %s references configMap %s, not rendered by the chart", name, n))
				}
			}
		}
		return out
	default:
		if !slices.Contains(allowedVolumes, source) {
			return []string{add("%s volume not allowed (%s)", source, name)}
		}
	}
	return nil
}

func containerViolations(o object, c container, refs podRefs) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf("%s: container %s: ", o.ref(), c.Name)+fmt.Sprintf(format, args...))
	}
	sc := c.SecurityContext
	if sc.Privileged != nil && *sc.Privileged {
		add("privileged not allowed")
	}
	if sc.AllowPrivilegeEscalation != nil && *sc.AllowPrivilegeEscalation {
		add("allowPrivilegeEscalation not allowed")
	}
	if len(sc.Capabilities.Add) > 0 {
		add("added capabilities not allowed (%s)", strings.Join(sc.Capabilities.Add, ", "))
	}
	if sc.RunAsUser != nil && *sc.RunAsUser == 0 {
		add("runAsUser 0 not allowed")
	}
	if sc.RunAsNonRoot != nil && !*sc.RunAsNonRoot {
		add("runAsNonRoot false not allowed")
	}
	if sc.ProcMount == "Unmasked" {
		add("procMount Unmasked not allowed")
	}
	if sc.SELinuxOptions.Type != "" {
		add("seLinuxOptions.type not allowed")
	}
	if sc.AppArmorProfile.Type == "Unconfined" {
		add("appArmorProfile Unconfined not allowed")
	}
	if sc.WindowsOptions.HostProcess != nil && *sc.WindowsOptions.HostProcess {
		add("windowsOptions.hostProcess not allowed")
	}
	for _, p := range c.Ports {
		if p.HostPort != 0 {
			add("hostPort %d not allowed", p.HostPort)
		}
	}
	for _, e := range c.Env {
		if e.ValueFrom == nil {
			continue
		}
		if r := e.ValueFrom.SecretKeyRef; r != nil && !refs.mountSecrets[r.Name] {
			add("env %s references secret %s, not rendered by the chart", e.Name, r.Name)
		}
		if r := e.ValueFrom.ConfigMapKeyRef; r != nil && !refs.rendered["ConfigMap/"+r.Name] {
			add("env %s references configMap %s, not rendered by the chart", e.Name, r.Name)
		}
	}
	for _, e := range c.EnvFrom {
		if r := e.SecretRef; r != nil && !refs.mountSecrets[r.Name] {
			add("envFrom references secret %s, not rendered by the chart", r.Name)
		}
		if r := e.ConfigMapRef; r != nil && !refs.rendered["ConfigMap/"+r.Name] {
			add("envFrom references configMap %s, not rendered by the chart", r.Name)
		}
	}
	return out
}

func primaryViolations(objs []object, primary string) []string {
	if primary == "" {
		return nil
	}
	for _, o := range objs {
		if o.Kind != "Service" || o.Metadata.Name != primary {
			continue
		}
		var s serviceSpec
		if err := o.Spec.Decode(&s); err == nil && slices.ContainsFunc(s.Ports, func(p servicePort) bool { return p.Port == 80 }) {
			return nil
		}
		return []string{"Service/" + primary + ": the primary Service must expose port 80"}
	}
	return []string{"Service/" + primary + ": primary Service (zaentrum.io/primary) is not rendered"}
}

// TestGuardrailCopy: each rule refuses a minimal manifest that breaks it, and
// what the platform hands an addon (events TLS secret, pull secrets, media
// claim) stays allowed.
func TestGuardrailCopy(t *testing.T) {
	in := guardInput{
		Namespace:       "zaentrum-example",
		Addon:           "example",
		MediaClaim:      "media",
		EventsTLSSecret: "event-client-tls",
		PullSecrets:     []string{"registry-pull"},
	}
	yes, no := true, false
	root := int64(0)

	t.Run("allowed", func(t *testing.T) {
		objs := manifests(t,
			obj("v1", "Secret", "worker-secrets", nil),
			obj("v1", "ConfigMap", "worker-config", nil),
			obj("v1", "PersistentVolumeClaim", "worker-data", map[string]any{"spec": map[string]any{"accessModes": []any{"ReadWriteOnce"}}}),
			obj("v1", "ServiceAccount", "worker", nil),
			obj("v1", "Service", "worker", map[string]any{"spec": map[string]any{"type": "ClusterIP", "ports": []any{map[string]any{"port": 80}}}}),
			workload("apps/v1", "Deployment", pod(map[string]any{
				"serviceAccountName": "worker",
				"imagePullSecrets":   []any{map[string]any{"name": "registry-pull"}, map[string]any{"name": "worker-secrets"}},
				"tolerations":        []any{map[string]any{"key": "example.org/dedicated", "operator": "Exists"}},
				"volumes": []any{
					map[string]any{"name": "tls", "secret": map[string]any{"secretName": "event-client-tls"}},
					map[string]any{"name": "media", "persistentVolumeClaim": map[string]any{"claimName": "media"}},
					map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "worker-data"}},
					map[string]any{"name": "config", "configMap": map[string]any{"name": "worker-config"}},
					map[string]any{"name": "scratch"},
					map[string]any{"name": "both", "projected": map[string]any{"sources": []any{
						map[string]any{"secret": map[string]any{"name": "worker-secrets"}},
						map[string]any{"configMap": map[string]any{"name": "worker-config"}},
					}}},
				},
			}, containerSpec(map[string]any{
				"env": []any{
					map[string]any{"name": "A", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "worker-secrets", "key": "a"}}},
					map[string]any{"name": "B", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "worker-config", "key": "b"}}},
				},
				"envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "registry-pull"}}},
			}))),
			workload("batch/v1", "Job", pod(nil, containerSpec(nil))),
		)
		allowed := in
		allowed.Primary = "worker"
		if v := violations(objs, allowed); len(v) > 0 {
			t.Errorf("want no refusals, got:\n  %s", strings.Join(v, "\n  "))
		}
	})

	cases := []struct {
		name string
		objs []map[string]any
		want string
	}{
		{"kind", list(obj("route.openshift.io/v1", "Route", "worker", nil)), "Route/worker: kind not allowed"},
		{"apiVersion", list(obj("extensions/v1beta1", "Deployment", "worker", nil)), "apiVersion extensions/v1beta1 not allowed"},
		{"no name", list(obj("v1", "ConfigMap", "", nil)), "object without kind or metadata.name"},
		{"duplicate", list(obj("v1", "ConfigMap", "worker", nil), obj("v1", "ConfigMap", "worker", nil)), "ConfigMap/worker: rendered more than once"},
		{"namespace", list(obj("v1", "ConfigMap", "worker", map[string]any{"metadata": map[string]any{"name": "worker", "namespace": "other"}})), "namespace other not allowed"},
		{"values object name", list(obj("v1", "Secret", "zaentrum-addon-example-values", nil)), "name reserved for the addon's values"},
		{"generated values name", list(obj("v1", "Secret", "zaentrum-addon-example-generated", nil)), "name reserved for the addon's values"},
		{"ownerReferences", list(obj("v1", "ConfigMap", "worker", map[string]any{"metadata": map[string]any{"name": "worker", "ownerReferences": []any{map[string]any{"kind": "Deployment", "name": "x"}}}})), "metadata.ownerReferences not allowed"},
		{"service-account annotation", list(obj("v1", "Secret", "worker", map[string]any{"metadata": map[string]any{"name": "worker", "annotations": map[string]any{"kubernetes.io/service-account.name": "platform"}}})), "annotation kubernetes.io/service-account.name not allowed"},
		{"service-account uid annotation", list(obj("v1", "ConfigMap", "worker", map[string]any{"metadata": map[string]any{"name": "worker", "annotations": map[string]any{"kubernetes.io/service-account.uid": "1"}}})), "annotation kubernetes.io/service-account.uid not allowed"},
		{"service-account token", list(obj("v1", "Secret", "worker", map[string]any{"type": "kubernetes.io/service-account-token"})), "Secret type kubernetes.io/service-account-token not allowed"},
		{"service type", list(service(map[string]any{"type": "NodePort"})), "Service type NodePort not allowed"},
		{"load balancer", list(service(map[string]any{"type": "LoadBalancer"})), "Service type LoadBalancer not allowed"},
		{"externalIPs", list(service(map[string]any{"externalIPs": []any{"192.0.2.1"}})), "spec.externalIPs not allowed"},
		{"loadBalancerSourceRanges", list(service(map[string]any{"loadBalancerSourceRanges": []any{"0.0.0.0/0"}})), "spec.loadBalancerSourceRanges not allowed"},
		{"externalName", list(service(map[string]any{"externalName": "db.example.org"})), "spec.externalName not allowed"},
		{"loadBalancerIP", list(service(map[string]any{"loadBalancerIP": "192.0.2.1"})), "spec.loadBalancerIP not allowed"},
		{"pvc volumeName", list(claim(map[string]any{"volumeName": "pv-1"})), "PVC spec.volumeName not allowed"},
		{"pvc dataSource", list(claim(map[string]any{"dataSource": map[string]any{"kind": "PersistentVolumeClaim", "name": "other"}})), "PVC spec.dataSource not allowed"},
		{"pvc dataSourceRef", list(claim(map[string]any{"dataSourceRef": map[string]any{"kind": "PersistentVolumeClaim", "name": "other"}})), "PVC spec.dataSourceRef not allowed"},
		{"hostNetwork", deploy(map[string]any{"hostNetwork": true}), "hostNetwork not allowed"},
		{"hostPID", deploy(map[string]any{"hostPID": true}), "hostPID not allowed"},
		{"hostIPC", deploy(map[string]any{"hostIPC": true}), "hostIPC not allowed"},
		{"job hostNetwork", list(workload("batch/v1", "Job", pod(map[string]any{"hostNetwork": true}, containerSpec(nil)))), "Job/worker: hostNetwork not allowed"},
		{"priorityClassName", deploy(map[string]any{"priorityClassName": "system-cluster-critical"}), "priorityClassName not allowed"},
		{"nodeName", deploy(map[string]any{"nodeName": "cp-1"}), "nodeName not allowed"},
		{"control-plane nodeSelector", deploy(map[string]any{"nodeSelector": map[string]any{"node-role.kubernetes.io/control-plane": ""}}), "nodeSelector node-role.kubernetes.io/control-plane not allowed"},
		{"master nodeSelector", deploy(map[string]any{"nodeSelector": map[string]any{"node-role.kubernetes.io/master": ""}}), "nodeSelector node-role.kubernetes.io/master not allowed"},
		{"control-plane toleration", deploy(map[string]any{"tolerations": []any{map[string]any{"key": "node-role.kubernetes.io/control-plane", "effect": "NoSchedule"}}}), "toleration for control-plane taints not allowed"},
		{"blanket toleration", deploy(map[string]any{"tolerations": []any{map[string]any{"operator": "Exists"}}}), "toleration for control-plane taints not allowed"},
		{"required control-plane affinity", deploy(map[string]any{"affinity": nodeAffinity("requiredDuringSchedulingIgnoredDuringExecution", map[string]any{"nodeSelectorTerms": []any{
			map[string]any{"matchExpressions": []any{map[string]any{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists"}}},
		}})}), "nodeAffinity for control-plane nodes not allowed"},
		{"preferred control-plane affinity", deploy(map[string]any{"affinity": nodeAffinity("preferredDuringSchedulingIgnoredDuringExecution", []any{
			map[string]any{"weight": 1, "preference": map[string]any{"matchFields": []any{map[string]any{"key": "node-role.kubernetes.io/master", "operator": "Exists"}}}},
		})}), "nodeAffinity for control-plane nodes not allowed"},
		{"pod seLinuxOptions.type", deploy(map[string]any{"securityContext": map[string]any{"seLinuxOptions": map[string]any{"type": "spc_t"}}}), "Deployment/worker: seLinuxOptions.type not allowed"},
		{"sysctls", deploy(map[string]any{"securityContext": map[string]any{"sysctls": []any{map[string]any{"name": "net.ipv4.ip_forward", "value": "1"}}}}), "sysctls not allowed"},
		{"pod appArmor", deploy(map[string]any{"securityContext": map[string]any{"appArmorProfile": map[string]any{"type": "Unconfined"}}}), "Deployment/worker: appArmorProfile Unconfined not allowed"},
		{"pod hostProcess", deploy(map[string]any{"securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}}), "Deployment/worker: windowsOptions.hostProcess not allowed"},
		{"pod root", deploy(map[string]any{"securityContext": map[string]any{"runAsUser": 0}}), "Deployment/worker: runAsUser 0 not allowed"},
		{"pod runAsNonRoot false", deploy(map[string]any{"securityContext": map[string]any{"runAsNonRoot": false}}), "Deployment/worker: runAsNonRoot false not allowed"},
		{"service account", deploy(map[string]any{"serviceAccountName": "platform"}), "serviceAccountName platform not allowed"},
		{"deprecated service account", deploy(map[string]any{"serviceAccount": "platform"}), "serviceAccountName platform not allowed"},
		{"pull secret", deploy(map[string]any{"imagePullSecrets": []any{map[string]any{"name": "platform-pull"}}}), "imagePullSecrets platform-pull not allowed"},
		{"events TLS secret as pull secret", deploy(map[string]any{"imagePullSecrets": []any{map[string]any{"name": "event-client-tls"}}}), "imagePullSecrets event-client-tls not allowed"},
		{"hostPath", deploy(map[string]any{"volumes": []any{map[string]any{"name": "host", "hostPath": map[string]any{"path": "/"}}}}), "hostPath volume not allowed (host)"},
		{"foreign claim", deploy(map[string]any{"volumes": []any{map[string]any{"name": "db", "persistentVolumeClaim": map[string]any{"claimName": "platform-db"}}}}), "persistentVolumeClaim platform-db not allowed"},
		{"foreign secret volume", deploy(map[string]any{"volumes": []any{map[string]any{"name": "s", "secret": map[string]any{"secretName": "platform-db"}}}}), "secret volume s references platform-db"},
		{"foreign configMap volume", deploy(map[string]any{"volumes": []any{map[string]any{"name": "c", "configMap": map[string]any{"name": "platform-config"}}}}), "configMap volume c references platform-config"},
		{"foreign projected secret", deploy(map[string]any{"volumes": []any{map[string]any{"name": "p", "projected": map[string]any{"sources": []any{map[string]any{"secret": map[string]any{"name": "platform-db"}}}}}}}), "projected volume p references secret platform-db"},
		{"foreign projected configMap", deploy(map[string]any{"volumes": []any{map[string]any{"name": "p", "projected": map[string]any{"sources": []any{map[string]any{"configMap": map[string]any{"name": "platform-config"}}}}}}}), "projected volume p references configMap platform-config"},
		{"privileged", deployContainer(map[string]any{"securityContext": map[string]any{"privileged": true}}), "container app: privileged not allowed"},
		{"privilege escalation", deployContainer(map[string]any{"securityContext": map[string]any{"allowPrivilegeEscalation": yes}}), "container app: allowPrivilegeEscalation not allowed"},
		{"added capabilities", deployContainer(map[string]any{"securityContext": map[string]any{"capabilities": map[string]any{"add": []any{"NET_ADMIN"}}}}), "container app: added capabilities not allowed (NET_ADMIN)"},
		{"container root", deployContainer(map[string]any{"securityContext": map[string]any{"runAsUser": root}}), "container app: runAsUser 0 not allowed"},
		{"container runAsNonRoot false", deployContainer(map[string]any{"securityContext": map[string]any{"runAsNonRoot": no}}), "container app: runAsNonRoot false not allowed"},
		{"procMount", deployContainer(map[string]any{"securityContext": map[string]any{"procMount": "Unmasked"}}), "container app: procMount Unmasked not allowed"},
		{"container seLinuxOptions.type", deployContainer(map[string]any{"securityContext": map[string]any{"seLinuxOptions": map[string]any{"type": "spc_t"}}}), "container app: seLinuxOptions.type not allowed"},
		{"container appArmor", deployContainer(map[string]any{"securityContext": map[string]any{"appArmorProfile": map[string]any{"type": "Unconfined"}}}), "container app: appArmorProfile Unconfined not allowed"},
		{"container hostProcess", deployContainer(map[string]any{"securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}}), "container app: windowsOptions.hostProcess not allowed"},
		{"hostPort", deployContainer(map[string]any{"ports": []any{map[string]any{"containerPort": 8080, "hostPort": 80}}}), "container app: hostPort 80 not allowed"},
		{"env secret", deployContainer(map[string]any{"env": []any{map[string]any{"name": "DB", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "platform-db", "key": "url"}}}}}), "env DB references secret platform-db"},
		{"env configMap", deployContainer(map[string]any{"env": []any{map[string]any{"name": "C", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "platform-config", "key": "c"}}}}}), "env C references configMap platform-config"},
		{"envFrom secret", deployContainer(map[string]any{"envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "platform-db"}}}}), "envFrom references secret platform-db"},
		{"envFrom configMap", deployContainer(map[string]any{"envFrom": []any{map[string]any{"configMapRef": map[string]any{"name": "platform-config"}}}}), "envFrom references configMap platform-config"},
		{"init container", list(workload("apps/v1", "Deployment", pod(map[string]any{"initContainers": []any{containerSpec(map[string]any{"name": "init", "securityContext": map[string]any{"privileged": true}})}}, containerSpec(nil)))), "container init: privileged not allowed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := violations(manifests(t, c.objs...), in)
			if !slices.ContainsFunc(got, func(v string) bool { return strings.Contains(v, c.want) }) {
				t.Errorf("want a refusal containing %q, got %q", c.want, got)
			}
		})
	}

	t.Run("primary", func(t *testing.T) {
		withPrimary := in
		withPrimary.Primary = "worker"
		if got := violations(nil, withPrimary); !slices.Contains(got, "Service/worker: primary Service (zaentrum.io/primary) is not rendered") {
			t.Errorf("missing primary: got %q", got)
		}
		objs := manifests(t, obj("v1", "Service", "worker", map[string]any{"spec": map[string]any{"ports": []any{map[string]any{"port": 8080}}}}))
		if got := violations(objs, withPrimary); !slices.Contains(got, "Service/worker: the primary Service must expose port 80") {
			t.Errorf("primary without port 80: got %q", got)
		}
	})
}

// ── manifest builders for TestGuardrailCopy ─────────────────────────────────

// manifests renders the given objects as one YAML stream and decodes it the
// way rendered charts are decoded.
func manifests(t *testing.T, objs ...map[string]any) []object {
	t.Helper()
	var docs []string
	for _, o := range objs {
		b, err := yaml.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, string(b))
	}
	out, err := decodeManifests(strings.Join(docs, "---\n"))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func list(objs ...map[string]any) []map[string]any { return objs }

// obj is an object of a kind; extra replaces or adds top-level fields.
func obj(apiVersion, kind, name string, extra map[string]any) map[string]any {
	o := map[string]any{"apiVersion": apiVersion, "kind": kind}
	if name != "" {
		o["metadata"] = map[string]any{"name": name}
	}
	for k, v := range extra {
		o[k] = v
	}
	return o
}

func service(spec map[string]any) map[string]any {
	return obj("v1", "Service", "worker", map[string]any{"spec": spec})
}

func claim(spec map[string]any) map[string]any {
	return obj("v1", "PersistentVolumeClaim", "worker-data", map[string]any{"spec": spec})
}

func workload(apiVersion, kind string, podSpec map[string]any) map[string]any {
	return obj(apiVersion, kind, "worker", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": podSpec}}})
}

// pod is a pod spec with one container; extra adds pod-level fields.
func pod(extra map[string]any, c map[string]any) map[string]any {
	p := map[string]any{"containers": []any{c}}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func containerSpec(extra map[string]any) map[string]any {
	c := map[string]any{"name": "app", "image": "example.org/app:1"}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func deploy(podExtra map[string]any) []map[string]any {
	return list(workload("apps/v1", "Deployment", pod(podExtra, containerSpec(nil))))
}

func deployContainer(containerExtra map[string]any) []map[string]any {
	return list(workload("apps/v1", "Deployment", pod(nil, containerSpec(containerExtra))))
}

func nodeAffinity(field string, value any) map[string]any {
	return map[string]any{"nodeAffinity": map[string]any{field: value}}
}
