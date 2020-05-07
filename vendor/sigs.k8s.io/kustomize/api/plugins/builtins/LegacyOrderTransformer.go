// Code generated by pluginator on LegacyOrderTransformer; DO NOT EDIT.
// pluginator {Version:unknown GitCommit:$Format:%H$ BuildDate:1970-01-01T00:00:00Z GoOs:linux GoArch:amd64}



package builtins

import (
	"sort"

	"github.com/pkg/errors"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/resource"
)

// Sort the resources using an ordering defined in the Gvk class.
// This puts cluster-wide basic resources with no
// dependencies (like Namespace, StorageClass, etc.)
// first, and resources with a high number of dependencies
// (like ValidatingWebhookConfiguration) last.
type LegacyOrderTransformerPlugin struct{}


// Nothing needed for configuration.
func (p *LegacyOrderTransformerPlugin) Config(
	h *resmap.PluginHelpers, c []byte) (err error) {
	return nil
}

func (p *LegacyOrderTransformerPlugin) Transform(m resmap.ResMap) (err error) {
	resources := make([]*resource.Resource, m.Size())
	ids := m.AllIds()
	sort.Sort(resmap.IdSlice(ids))
	for i, id := range ids {
		resources[i], err = m.GetByCurrentId(id)
		if err != nil {
			return errors.Wrap(err, "expected match for sorting")
		}
	}
	m.Clear()
	for _, r := range resources {
		m.Append(r)
	}
	return nil
}

func NewLegacyOrderTransformerPlugin() resmap.TransformerPlugin {
  return &LegacyOrderTransformerPlugin{}
}
