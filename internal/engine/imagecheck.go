package engine

import (
	"strings"
)

// ImageState is the outcome of an image update check.
type ImageState int

// Image states.
const (
	ImageUnknown  ImageState = iota // not checked yet, or the check failed
	ImageUpToDate                   // the source offers what runs
	ImageOutdated                   // the source offers a newer build
	ImageLocal                      // no remote source: built, imported or untagged
)

func (s ImageState) String() string {
	switch s {
	case ImageUpToDate:
		return "up to date"
	case ImageOutdated:
		return "update available"
	case ImageLocal:
		return "local"
	}
	return "unknown"
}

// ImageCheck names one image source. Instances created from the same
// source share a check, so a source is queried once however many
// instances use it. It is comparable and serves as a map key.
type ImageCheck struct {
	Engine ID
	// Ref is the reference to check. Docker: the reference the container
	// was created from ("nginx:1.27"). Incus: the simplestreams alias
	// built from the image.* keys ("images:debian/bookworm/default").
	Ref string
	// Arch is the Incus image architecture ("amd64"); "" for Docker.
	Arch string
	// VM selects the Incus virtual-machine variant.
	VM bool
	// Local is the Incus base image fingerprint the instance was created
	// from; "" for Docker, whose local image is what Ref resolves to.
	Local string
}

// ImageStatus is the answer of ImageUpdateStatus.
type ImageStatus struct {
	Check ImageCheck
	State ImageState
	// Current and Available identify the local and the offered builds:
	// Docker manifest digests ("sha256:…"), Incus fingerprints. Empty
	// when unknown (a local image has no Available).
	Current, Available string
	// Serial and NewSerial are Incus build serials ("20261020_05:24"),
	// for display; empty for Docker.
	Serial, NewSerial string
}

// CheckOf returns the image check of an instance, or false when it has
// no remote source to compare with (an image pulled by digest, a local
// Incus image).
func CheckOf(in Instance) (ImageCheck, bool) {
	switch x := in.Extra.(type) {
	case *DockerExtra:
		ref := strings.TrimSpace(in.Image)
		if ref == "" || strings.HasPrefix(ref, "sha256:") || strings.Contains(ref, "@") {
			return ImageCheck{}, false
		}
		return ImageCheck{Engine: Docker, Ref: ref}, true
	case *IncusExtra:
		if x.ImageAlias == "" {
			return ImageCheck{}, false
		}
		return ImageCheck{Engine: Incus, Ref: x.ImageAlias, Arch: x.ImageArch, VM: in.Kind == KindVM, Local: x.BaseImage}, true
	}
	return ImageCheck{}, false
}

// IncusAlias builds the "images:<os>/<release>/<variant>" alias that
// images.linuxcontainers.org publishes for every product, from the image.*
// keys Incus copies into an instance. It returns "" when they are missing.
func IncusAlias(os, release, variant string) string {
	os, release = strings.ToLower(strings.TrimSpace(os)), strings.TrimSpace(release)
	if os == "" || release == "" || strings.ContainsAny(os+release+variant, "/: ") {
		return ""
	}
	if variant = strings.TrimSpace(variant); variant == "" {
		variant = "default"
	}
	return "images:" + os + "/" + release + "/" + variant
}
