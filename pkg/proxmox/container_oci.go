package proxmox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	pve "github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const ociTemplateNamePrefix = "kubemox-oci-"

// TemplateContainerName returns the Proxmox source template name when using template.name (non-OCI).
// For OCI (template.image) kubemox creates the user container directly from the pulled ostemplate volume;
// this helper is only relevant for the name-based clone path.
func TemplateContainerName(t proxmoxv1alpha1.ContainerTemplate) string {
	if t.Image != nil && t.Image.Reference != "" {
		return ociTemplateHostname(t.Image.Reference)
	}
	return t.Name
}

func ociImageID(imageRef string) string {
	sum := sha256.Sum256([]byte(imageRef))
	return hex.EncodeToString(sum[:8])
}

// ociTemplateHostname is the Proxmox CT name for the template (stable, DNS-safe).
func ociTemplateHostname(imageRef string) string {
	return ociTemplateNamePrefix + ociImageID(imageRef)
}

// ociPullFilename is the base name passed to oci-registry-pull (Proxmox adds an extension such as .tar).
// It is derived from the image reference so the stored file is recognizable
// (e.g. nginx-1.28.3, not kubemox-oci-<hash>).
func ociPullFilename(imageRef string) string {
	base := ociReferenceToFileBase(imageRef)
	if base != "" {
		return base
	}
	return ociTemplateNamePrefix + ociImageID(imageRef)
}

// OCIImageProxmoxTag builds one Proxmox tag from the OCI reference (charset-safe, length-limited).
// Proxmox tags use semicolons between values; a single image maps to one tag prefixed with img-.
func OCIImageProxmoxTag(imageRef string) string {
	ref := strings.TrimSpace(imageRef)
	if ref == "" {
		return ""
	}
	base := ociReferenceToFileBase(ref)
	if base == "" {
		base = "x-" + ociImageID(ref)
	}
	tag := "img-" + base
	if len(tag) > 120 {
		tag = tag[:120]
		tag = strings.TrimRight(tag, "-.")
	}
	return tag
}

// LXCHasOCIImageTag reports whether Proxmox tags (semicolon-separated) include the tag for imageRef.
func LXCHasOCIImageTag(tagsField string, imageRef string) bool {
	want := OCIImageProxmoxTag(imageRef)
	if want == "" {
		return false
	}
	for _, t := range strings.Split(tagsField, ";") {
		if strings.TrimSpace(t) == want {
			return true
		}
	}
	return false
}

// LXCHasAnyOCIImageTag reports whether Proxmox tags include an image tag at all.
func LXCHasAnyOCIImageTag(tagsField string) bool {
	for _, t := range strings.Split(tagsField, ";") {
		if strings.HasPrefix(strings.TrimSpace(t), "img-") {
			return true
		}
	}
	return false
}

func ociReferenceToFileBase(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ':', r == '/', r == '@', r == '.', r == '_', r == '-':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-.")
	if len(s) > 200 {
		s = s[:200]
		s = strings.TrimRight(s, "-.")
	}
	return strings.ToLower(s)
}

// ociArtifactStorage returns the pool used for oci-registry-pull and listing OCI content.
// template.image requires an explicit file-based pool; disk[0].storage is never used for OCI pulls
// (it is often lvmthin and cannot store OCI uploads).
func ociArtifactStorage(ct *proxmoxv1alpha1.Container) (string, error) {
	if ct.Spec.Template.Image == nil || ct.Spec.Template.Image.Storage == "" {
		return "", fmt.Errorf("template.image.storage is required when using template.image " +
			"(file-based Proxmox datastore; lvmthin cannot store OCI images; template.disk keeps rootfs)")
	}
	return ct.Spec.Template.Image.Storage, nil
}

func firstRootFS(ct *proxmoxv1alpha1.Container) (string, error) {
	if len(ct.Spec.Template.Disk) == 0 {
		return "", fmt.Errorf("template.disk is required for a container created from a volume")
	}
	d := ct.Spec.Template.Disk[0]
	if d.Storage == "" || d.Size < 1 {
		return "", fmt.Errorf("template.disk[0] must set storage and size")
	}
	return fmt.Sprintf("%s:%d", d.Storage, d.Size), nil
}

func ociBasenameFromVolid(volid string) string {
	i := strings.LastIndex(volid, "/")
	if i < 0 {
		return volid
	}
	return volid[i+1:]
}

func ociStripArchiveExt(name string) string {
	name = strings.ToLower(name)
	for _, suf := range []string{".tar.zst", ".tar", ".tgz", ".zst"} {
		if strings.HasSuffix(name, suf) {
			return strings.TrimSuffix(name, suf)
		}
	}
	return name
}

func ociStemNormalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ".", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// ociVolMatchesPull matches a storage row to the oci-registry-pull filename stem (e.g. nginx-1-27-1).
func ociVolMatchesPull(volid, filenameStem string) bool {
	if filenameStem == "" || volid == "" {
		return false
	}
	fs := strings.ToLower(filenameStem)
	lv := strings.ToLower(volid)
	if strings.Contains(lv, fs) {
		return true
	}
	base := ociStripArchiveExt(ociBasenameFromVolid(volid))
	return ociStemNormalize(base) == ociStemNormalize(fs)
}

func (pc *ProxmoxClient) findOCIVolumeID(nodeName, storage, filenameMarker string) (string, error) {
	path := fmt.Sprintf("/nodes/%s/storage/%s/content", nodeName, storage)
	var items []struct {
		Volid   string `json:"volid"`
		Content string `json:"content,omitempty"`
	}
	if err := pc.Client.Get(ctx, path, &items); err != nil {
		return "", err
	}
	var ociHits, vzHits []string
	for _, it := range items {
		v := it.Volid
		if !ociVolMatchesPull(v, filenameMarker) {
			continue
		}
		lv := strings.ToLower(v)
		switch {
		case strings.Contains(lv, ":oci/"):
			ociHits = append(ociHits, v)
		case strings.Contains(lv, ":vztmpl/"):
			vzHits = append(vzHits, v)
		}
	}
	switch {
	case len(ociHits) == 1:
		return ociHits[0], nil
	case len(ociHits) > 1:
		for _, v := range ociHits {
			if strings.Contains(strings.ToLower(v), "/"+strings.ToLower(filenameMarker)) {
				return v, nil
			}
		}
		return ociHits[0], nil
	case len(vzHits) >= 1:
		// Some Proxmox builds list OCI pulls under vztmpl in API/UI; ostemplate still works for create.
		log.Log.Info(fmt.Sprintf("OCI artifact for %q found under vztmpl (not :oci/): %s", filenameMarker, vzHits[0]))
		return vzHits[0], nil
	default:
		return "", fmt.Errorf("no OCI/vztmpl volume matching %q on storage %s: %w", filenameMarker, storage, pve.ErrNotFound)
	}
}

func (pc *ProxmoxClient) ociRegistryPull(nodeName, storage, reference, filename string) error {
	var upid pve.UPID
	payload := map[string]string{
		"reference": reference,
		"filename":  filename,
	}
	if err := pc.Client.Post(ctx,
		fmt.Sprintf("/nodes/%s/storage/%s/oci-registry-pull", nodeName, storage),
		payload, &upid); err != nil {
		return err
	}
	task := pve.NewTask(upid, pc.Client)
	ok, done, err := task.WaitForCompleteStatus(ctx, 10, 5)
	if !ok {
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !done {
		return err
	}
	return nil
}

// ociArtifactAlreadyExistsError is returned when oci-registry-pull refuses to overwrite an existing blob.
func ociArtifactAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "refusing to override") || strings.Contains(msg, "existing file")
}

// ensureOCIVolume pulls the image if needed and returns the storage volid used as ostemplate.
func (pc *ProxmoxClient) ensureOCIVolume(ct *proxmoxv1alpha1.Container) (string, error) {
	image := ct.Spec.Template.Image.Reference
	nodeName := ct.Spec.NodeName
	storage, err := ociArtifactStorage(ct)
	if err != nil {
		return "", err
	}
	filename := ociPullFilename(image)

	volid, err := pc.findOCIVolumeID(nodeName, storage, filename)
	if err != nil && !errors.Is(err, pve.ErrNotFound) {
		return "", err
	}
	if errors.Is(err, pve.ErrNotFound) || volid == "" {
		log.Log.Info(fmt.Sprintf("Pulling OCI image %s to storage %s", image, storage))
		pullErr := pc.ociRegistryPull(nodeName, storage, image, filename)
		if pullErr != nil && !ociArtifactAlreadyExistsError(pullErr) {
			return "", pullErr
		}
		if pullErr != nil && ociArtifactAlreadyExistsError(pullErr) {
			log.Log.Info(fmt.Sprintf("OCI blob already on storage %s, reusing (%v)", storage, pullErr))
		}
		volid, err = pc.findOCIVolumeID(nodeName, storage, filename)
		if err != nil {
			return "", fmt.Errorf("OCI image not on storage %s after pull or existing-file skip: %w", storage, err)
		}
	}
	return volid, nil
}

// newContainerOptions are the parameters to create ct from the volume volid.
func newContainerOptions(ct *proxmoxv1alpha1.Container, volid, hostname string) ([]pve.ContainerOption, error) {
	rootfs, err := firstRootFS(ct)
	if err != nil {
		return nil, err
	}
	cores := ct.Spec.Template.Cores
	if cores < 1 {
		cores = 1
	}
	mem := ct.Spec.Template.Memory
	if mem < 16 {
		mem = 512
	}
	imageTag := ""
	if ct.Spec.Template.Image != nil {
		imageTag = OCIImageProxmoxTag(ct.Spec.Template.Image.Reference)
	}
	opts := []pve.ContainerOption{
		{Name: "hostname", Value: hostname},
		{Name: "ostemplate", Value: volid},
		{Name: "rootfs", Value: rootfs},
		{Name: "memory", Value: mem},
		{Name: "cores", Value: cores},
		{Name: "start", Value: 0},
		{Name: containerOptionTags, Value: withContainerUIDTag(imageTag, ct)},
	}
	opts = append(opts, containerNetOptions(ct)...)
	opts = append(opts, containerGuestOptions(ct)...)
	return append(opts, containerCreateOnlyOptions(ct)...), nil
}

// CreateContainerFromOCIImage creates a single LXC from the pulled OCI ostemplate volume and returns its VMID.
// Proxmox supports pct create with ostemplate pointing at the same vztmpl/oci blob for each instance;
// no intermediate template CT or clone step is required (avoids leaving kubemox-oci-* plus the workload CT).
func (pc *ProxmoxClient) CreateContainerFromOCIImage(ct *proxmoxv1alpha1.Container) (int, error) {
	volid, err := pc.ensureOCIVolume(ct)
	if err != nil {
		return 0, err
	}
	return pc.createContainerFromVolume(ct, volid)
}

// createContainerFromVolume creates ct from a container template or OCI
// archive on a storage and returns its VMID.
func (pc *ProxmoxClient) createContainerFromVolume(ct *proxmoxv1alpha1.Container, volid string) (int, error) {
	nodeName := ct.Spec.NodeName
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return 0, err
	}
	host := ct.Spec.Name
	opts, err := newContainerOptions(ct, volid, host)
	if err != nil {
		return 0, err
	}
	log.Log.Info(fmt.Sprintf("Creating container %s from %s", host, volid))
	// The VMID is picked here rather than by Proxmox so that it is known
	// without looking the hostname up, which need not be unique. The mutex
	// keeps a concurrent VM create from picking the same one.
	var id int
	var createTask *pve.Task
	func() {
		mutex.Lock()
		defer mutex.Unlock()
		if id, err = getNextVMID(pc.Client); err == nil {
			createTask, err = node.NewContainer(ctx, id, opts...)
		}
	}()
	if err != nil {
		log.Log.Error(err, "Failed to create container from a volume")
		return 0, err
	}
	ok, done, err := createTask.WaitForCompleteStatus(ctx, 360, 5)
	if !ok {
		return 0, &TaskError{ExitStatus: createTask.ExitStatus}
	}
	if !done {
		return 0, err
	}
	return id, nil
}
