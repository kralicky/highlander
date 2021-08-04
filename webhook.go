package highlander

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var ErrThereCanBeOnlyOne = errors.New(
	"there can be only one instance of this object per namespace")

type Webhook struct {
	object client.Object
	log    logr.Logger
	gvk    schema.GroupVersionKind
	mgr    manager.Manager
	cli    client.Client
}

func NewFor(apiType client.Object) *Webhook {
	return &Webhook{
		object: apiType,
	}
}

func (w *Webhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create {
		return admission.Allowed("")
	}
	gvk := req.Kind
	if gvk.Group != w.gvk.Group ||
		gvk.Version != w.gvk.Version ||
		gvk.Kind != w.gvk.Kind {
		return admission.Allowed("")
	}

	if err := w.ValidateCreate(); err != nil {
		if errors.Is(err, ErrThereCanBeOnlyOne) {
			return admission.Denied(err.Error())
		} else {
			return admission.Errored(http.StatusBadRequest, err)
		}
	}

	return admission.Allowed("")
}

func (w *Webhook) SetupWithManager(mgr manager.Manager) error {
	w.mgr = mgr
	w.cli = mgr.GetClient()
	w.log = mgr.GetLogger()

	var err error
	w.gvk, err = apiutil.GVKForObject(w.object, mgr.GetScheme())
	if err != nil {
		return err
	}

	path := generateValidatePath(w.gvk)
	wh := &admission.Webhook{
		Handler: w,
	}
	wh.InjectLogger(w.log)
	wh.InjectScheme(mgr.GetScheme())
	mgr.GetWebhookServer().Register(path, wh)
	return nil
}

func (w *Webhook) ValidateCreate() error {
	// Check if any other instances of this gvk exist in the same namespace
	ul := unstructured.UnstructuredList{}
	ul.SetGroupVersionKind(w.gvk)
	err := w.cli.List(context.Background(), &ul, &client.ListOptions{
		Namespace: w.object.GetNamespace(),
	})
	if err != nil {
		w.log.Error(err, "Failed to list objects in namespace",
			"namespace", w.object.GetNamespace(),
		)
		return err
	}
	if len(ul.Items) > 0 {
		if ul.Items[0].GetDeletionTimestamp() != nil {
			// Old object is being deleted, allow the new one to be created
			return nil
		}
		return ErrThereCanBeOnlyOne
	}
	return nil
}

func generateValidatePath(gvk schema.GroupVersionKind) string {
	return "/highlander-" + strings.ReplaceAll(gvk.Group, ".", "-") + "-" +
		gvk.Version + "-" + strings.ToLower(gvk.Kind)
}
