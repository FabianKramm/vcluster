package generic

import (
	"fmt"
	"strings"

	"github.com/loft-sh/vcluster/pkg/mappings"
	"github.com/loft-sh/vcluster/pkg/scheme"
	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// PhysicalNameWithObjectFunc is a definition to translate a name that also optionally expects a vObj
type PhysicalNameWithObjectFunc func(ctx *synccontext.SyncContext, vName, vNamespace string, vObj client.Object) types.NamespacedName

// PhysicalNameFunc is a definition to translate a name
type PhysicalNameFunc func(ctx *synccontext.SyncContext, vName, vNamespace string) types.NamespacedName

// NewMapper creates a new mapper with a custom physical name func
func NewMapper(ctx *synccontext.RegisterContext, obj client.Object, translateName PhysicalNameFunc) (synccontext.Mapper, error) {
	return NewMapperWithObject(ctx, obj, func(ctx *synccontext.SyncContext, vName, vNamespace string, _ client.Object) types.NamespacedName {
		return translateName(ctx, vName, vNamespace)
	})
}

// NewMapperWithObject creates a new mapper with a custom physical name func
func NewMapperWithObject(ctx *synccontext.RegisterContext, obj client.Object, translateName PhysicalNameWithObjectFunc) (synccontext.Mapper, error) {
	return newMapper(ctx, obj, true, translateName)
}

// NewMapperWithoutRecorder creates a new mapper with a recorder to store mappings in the mappings store
func NewMapperWithoutRecorder(ctx *synccontext.RegisterContext, obj client.Object, translateName PhysicalNameWithObjectFunc) (synccontext.Mapper, error) {
	return newMapper(ctx, obj, false, translateName)
}

// newMapper creates a new mapper with a recorder to store mappings in the mappings store
func newMapper(ctx *synccontext.RegisterContext, obj client.Object, recorder bool, translateName PhysicalNameWithObjectFunc) (synccontext.Mapper, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme.Scheme)
	if err != nil {
		return nil, fmt.Errorf("retrieve GVK for object failed: %w", err)
	}

	var retMapper synccontext.Mapper = &mapper{
		translateName: translateName,
		virtualClient: ctx.VirtualManager.GetClient(),
		obj:           obj,
		gvk:           gvk,
	}
	if recorder {
		retMapper = WithRecorder(retMapper)
	}
	return retMapper, nil
}

type mapper struct {
	translateName PhysicalNameWithObjectFunc
	virtualClient client.Client

	obj client.Object
	gvk schema.GroupVersionKind
}

func (n *mapper) GroupVersionKind() schema.GroupVersionKind {
	return n.gvk
}

func (n *mapper) Migrate(ctx *synccontext.RegisterContext, mapper synccontext.Mapper) error {
	gvk := mapper.GroupVersionKind()
	listGvk := schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	}

	list, err := scheme.Scheme.New(listGvk)
	if err != nil {
		if !runtime.IsNotRegisteredError(err) {
			return fmt.Errorf("migrate create object list %s: %w", listGvk.String(), err)
		}

		list = &unstructured.UnstructuredList{}
	}

	uList, ok := list.(*unstructured.UnstructuredList)
	if ok {
		uList.SetKind(listGvk.Kind)
		uList.SetAPIVersion(listGvk.GroupVersion().String())
	}

	// it's safe to list here without namespace as this will just list all items in the cache
	err = ctx.VirtualManager.GetClient().List(ctx, list.(client.ObjectList))
	if err != nil {
		return fmt.Errorf("error listing %s: %w", listGvk.String(), err)
	}

	items, err := meta.ExtractList(list)
	if err != nil {
		return fmt.Errorf("extract list %s: %w", listGvk.String(), err)
	}

	for _, item := range items {
		clientObject, ok := item.(client.Object)
		if !ok {
			continue
		}

		vName := types.NamespacedName{Name: clientObject.GetName(), Namespace: clientObject.GetNamespace()}
		pName := mapper.VirtualToHost(ctx.ToSyncContext("migrate-"+listGvk.Kind), vName, clientObject)
		if pName.Name != "" {
			nameMapping := synccontext.NameMapping{
				GroupVersionKind: n.gvk,
				VirtualName:      vName,
				HostName:         pName,
			}

			err = ctx.Mappings.Store().RecordAndSaveReference(ctx, nameMapping, nameMapping)
			if err != nil {
				return fmt.Errorf("error saving reference in store: %w", err)
			}
		}
	}

	return nil
}

func (n *mapper) VirtualToHost(ctx *synccontext.SyncContext, req types.NamespacedName, vObj client.Object) (retName types.NamespacedName) {
	return n.translateName(ctx, req.Name, req.Namespace, vObj)
}

func (n *mapper) HostToVirtual(ctx *synccontext.SyncContext, req types.NamespacedName, pObj client.Object) (retName types.NamespacedName) {
	if pObj != nil {
		pAnnotations := pObj.GetAnnotations()
		if pAnnotations[translate.NameAnnotation] != "" {
			// check if kind matches
			gvk, ok := pAnnotations[translate.KindAnnotation]
			if !ok || n.gvk.String() == gvk {
				return types.NamespacedName{
					Namespace: pAnnotations[translate.NamespaceAnnotation],
					Name:      pAnnotations[translate.NameAnnotation],
				}
			}
		}
	}

	return TryToTranslateBack(ctx, req, n.gvk)
}

// TryToTranslateBack is used to find out the name mapping automatically in certain scenarios, this doesn't always
// work, but for some cases this is pretty useful.
func TryToTranslateBack(ctx *synccontext.SyncContext, req types.NamespacedName, gvk schema.GroupVersionKind) types.NamespacedName {
	if ctx == nil || ctx.Config == nil || ctx.Mappings == nil || !ctx.Mappings.Has(mappings.Namespaces()) {
		return types.NamespacedName{}
	}

	// if multi-namespace mode we try to translate back
	if ctx.Config.Experimental.MultiNamespaceMode.Enabled {
		if gvk == mappings.Namespaces() {
			return types.NamespacedName{}
		}

		// get namespace mapper
		namespaceMapper, err := ctx.Mappings.ByGVK(mappings.Namespaces())
		if err != nil {
			return types.NamespacedName{}
		}

		vNamespace := namespaceMapper.HostToVirtual(ctx, types.NamespacedName{Name: req.Namespace}, nil)
		if vNamespace.Name == "" {
			return types.NamespacedName{}
		}

		klog.FromContext(ctx).V(1).Info("Translated back name/namespace via multi-namespace mode method", "req", req.String(), "ret", types.NamespacedName{
			Namespace: vNamespace.Name,
			Name:      req.Name,
		}.String())
		return types.NamespacedName{
			Namespace: vNamespace.Name,
			Name:      req.Name,
		}
	}

	// if single namespace mode and the owner object was translated via NameShort, we can try to find that name
	// within the host name and assume it's the same namespace / name
	nameMapping, ok := synccontext.MappingFrom(ctx)
	if !ok || nameMapping.VirtualName.Namespace == "" {
		return types.NamespacedName{}
	} else if translate.Default.HostNameShort(ctx, nameMapping.VirtualName.Name, nameMapping.VirtualName.Namespace).String() != nameMapping.HostName.String() {
		return types.NamespacedName{}
	}

	// test if the name is part of the host name
	if !strings.Contains(req.Name, nameMapping.HostName.Name) {
		return types.NamespacedName{}
	}

	vNamespace := nameMapping.VirtualName.Namespace
	vName := strings.Replace(req.Name, nameMapping.HostName.Name, nameMapping.VirtualName.Name, -1)
	klog.FromContext(ctx).V(1).Info("Translated back name/namespace via single-namespace mode method", "req", req.String(), "ret", types.NamespacedName{
		Namespace: vNamespace,
		Name:      vName,
	}.String())
	return types.NamespacedName{
		Name:      vName,
		Namespace: vNamespace,
	}
}

func (n *mapper) IsManaged(ctx *synccontext.SyncContext, pObj client.Object) (bool, error) {
	return translate.Default.IsManaged(ctx, pObj), nil
}
