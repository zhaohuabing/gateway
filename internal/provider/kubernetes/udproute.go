// Portions of this code are based on code from Contour, available at:
// https://github.com/projectcontour/contour/blob/main/internal/controller/udproute.go

package kubernetes

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwapiv1b1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"github.com/envoyproxy/gateway/internal/envoygateway/config"
	"github.com/envoyproxy/gateway/internal/gatewayapi"
	"github.com/envoyproxy/gateway/internal/message"
	"github.com/envoyproxy/gateway/internal/provider/utils"
	"github.com/envoyproxy/gateway/internal/status"
)

const (
	serviceUDPRouteIndex = "serviceUDPRouteBackendRef"
)

type udpRouteReconciler struct {
	client          client.Client
	log             logr.Logger
	statusUpdater   status.Updater
	classController gwapiv1b1.GatewayController

	resources *message.ProviderResources
}

// newUDPRouteController creates the udproute controller from mgr. The controller will be pre-configured
// to watch for UDPRoute objects across all namespaces.
func newUDPRouteController(mgr manager.Manager, cfg *config.Server, su status.Updater, resources *message.ProviderResources) error {
	r := &udpRouteReconciler{
		client:          mgr.GetClient(),
		log:             cfg.Logger,
		classController: gwapiv1b1.GatewayController(cfg.EnvoyGateway.Gateway.ControllerName),
		statusUpdater:   su,
		resources:       resources,
	}

	c, err := controller.New("udproute", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}
	r.log.Info("created udproute controller")

	if err := c.Watch(
		&source.Kind{Type: &gwapiv1a2.UDPRoute{}},
		&handler.EnqueueRequestForObject{},
	); err != nil {
		return err
	}

	// Subscribe to status updates
	go r.subscribeAndUpdateStatus(context.Background())

	// Add indexing on UDPRoute, for Service objects that are referenced in UDPRoute objects
	// via `.spec.rules.backendRefs`. This helps in querying for UDPRoutes that are affected by
	// a particular Service CRUD.
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &gwapiv1a2.UDPRoute{}, serviceUDPRouteIndex, func(rawObj client.Object) []string {
		udpRoute := rawObj.(*gwapiv1a2.UDPRoute)
		var backendServices []string
		for _, rule := range udpRoute.Spec.Rules {
			for _, backend := range rule.BackendRefs {
				if string(*backend.Kind) == gatewayapi.KindService {
					// If an explicit Service namespace is not provided, use the UDPRoute namespace to
					// lookup the provided Service Name.
					backendServices = append(backendServices,
						types.NamespacedName{
							Namespace: gatewayapi.NamespaceDerefOrAlpha(backend.Namespace, udpRoute.Namespace),
							Name:      string(backend.Name),
						}.String(),
					)
				}
			}
		}
		return backendServices
	}); err != nil {
		return err
	}

	// Watch Gateway CRUDs and reconcile affected UDPRoutes.
	if err := c.Watch(
		&source.Kind{Type: &gwapiv1b1.Gateway{}},
		handler.EnqueueRequestsFromMapFunc(r.getUDPRoutesForGateway),
	); err != nil {
		return err
	}

	// Watch Service CRUDs and reconcile affected UDPRoutes.
	if err := c.Watch(
		&source.Kind{Type: &corev1.Service{}},
		handler.EnqueueRequestsFromMapFunc(r.getUDPRoutesForService),
	); err != nil {
		return err
	}

	r.log.Info("watching udproute objects")
	return nil
}

// getUDPRoutesForGateway uses a Gateway obj to fetch UDPRoutes, iterating
// through them and creating a reconciliation request for each valid UDPRoute
// that references obj.
func (r *udpRouteReconciler) getUDPRoutesForGateway(obj client.Object) []reconcile.Request {
	ctx := context.Background()

	gw, ok := obj.(*gwapiv1b1.Gateway)
	if !ok {
		r.log.Info("unexpected object type, bypassing reconciliation", "object", obj)
		return []reconcile.Request{}
	}

	routes := &gwapiv1a2.UDPRouteList{}
	if err := r.client.List(ctx, routes); err != nil {
		return []reconcile.Request{}
	}

	requests := []reconcile.Request{}
	for i := range routes.Items {
		route := routes.Items[i]
		gateways, err := validateParentRefs(ctx, r.client, route.Namespace, r.classController, gatewayapi.UpgradeParentReferences(route.Spec.ParentRefs))
		if err != nil {
			r.log.Info("invalid parentRefs for udproute, bypassing reconciliation", "object", obj)
			continue
		}
		for j := range gateways {
			if gateways[j].Namespace == gw.Namespace && gateways[j].Name == gw.Name {
				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: route.Namespace,
						Name:      route.Name,
					},
				}
				requests = append(requests, req)
				break
			}
		}
	}

	return requests
}

// getUDPRoutesForService uses a Service obj to fetch UDPRoutes that references
// the Service using `.spec.rules.backendRefs`. The affected UDPRoutes are then
// pushed for reconciliation.
func (r *udpRouteReconciler) getUDPRoutesForService(obj client.Object) []reconcile.Request {
	affectedUDPRouteList := &gwapiv1a2.UDPRouteList{}

	if err := r.client.List(context.Background(), affectedUDPRouteList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(serviceUDPRouteIndex, utils.NamespacedName(obj).String()),
	}); err != nil {
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(affectedUDPRouteList.Items))
	for i, item := range affectedUDPRouteList.Items {
		requests[i] = reconcile.Request{
			NamespacedName: utils.NamespacedName(item.DeepCopy()),
		}
	}

	return requests
}

func (r *udpRouteReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithValues("namespace", request.Namespace, "name", request.Name)

	log.Info("reconciling udproute")

	// Fetch all UDPRoutes from the cache.
	routeList := &gwapiv1a2.UDPRouteList{}
	if err := r.client.List(ctx, routeList); err != nil {
		return reconcile.Result{}, fmt.Errorf("error listing udproutes")
	}

	found := false
	for i := range routeList.Items {
		// See if this route from the list matched the reconciled route.
		route := routeList.Items[i]
		routeKey := utils.NamespacedName(&route)
		if routeKey == request.NamespacedName {
			found = true
		}

		// Store the udproute in the resource map.
		r.resources.UDPRoutes.Store(routeKey, &route)
		log.Info("added udproute to resource map")

		// Get the route's namespace from the cache.
		nsKey := types.NamespacedName{Name: route.Namespace}
		ns := new(corev1.Namespace)
		if err := r.client.Get(ctx, nsKey, ns); err != nil {
			if errors.IsNotFound(err) {
				// The route's namespace doesn't exist in the cache, so remove it from
				// the namespace resource map if it exists.
				if _, ok := r.resources.Namespaces.Load(nsKey.Name); ok {
					r.resources.Namespaces.Delete(nsKey.Name)
					log.Info("deleted namespace from resource map")
				}
			}
			return reconcile.Result{}, fmt.Errorf("failed to get namespace %s", nsKey.Name)
		}

		// The route's namespace exists, so add it to the resource map.
		r.resources.Namespaces.Store(nsKey.Name, ns)
		log.Info("added namespace to resource map")

		// Get the route's backendRefs from the cache. Note that a Service is the
		// only supported kind.
		for i := range route.Spec.Rules {
			for j := range route.Spec.Rules[i].BackendRefs {
				ref := route.Spec.Rules[i].BackendRefs[j]
				if err := validateUDPRouteBackendRef(&ref); err != nil {
					return reconcile.Result{}, fmt.Errorf("invalid backendRef: %w", err)
				}

				// The backendRef is valid, so get the referenced service from the cache.
				svcKey := types.NamespacedName{Namespace: route.Namespace, Name: string(ref.Name)}
				svc := new(corev1.Service)
				if err := r.client.Get(ctx, svcKey, svc); err != nil {
					if errors.IsNotFound(err) {
						// The ref's service doesn't exist in the cache, so remove it from
						// the resource map if it exists.
						if _, ok := r.resources.Services.Load(svcKey); ok {
							r.resources.Services.Delete(svcKey)
							log.Info("deleted service from resource map")
						}
					}
					return reconcile.Result{}, fmt.Errorf("failed to get service %s/%s",
						svcKey.Namespace, svcKey.Name)
				}

				// The backendRef Service exists, so add it to the resource map.
				r.resources.Services.Store(svcKey, svc)
				log.Info("added service to resource map")
			}
		}
	}

	if !found {
		// Delete the udproute from the resource map.
		r.resources.UDPRoutes.Delete(request.NamespacedName)
		log.Info("deleted udproute from resource map")

		// Delete the Namespace and Service from the resource maps if no other
		// routes (UDPRoute or HTTPRoute) exist in the namespace.
		found, err := isRoutePresentInNamespace(ctx, r.client, request.NamespacedName.Namespace)
		if err != nil {
			return reconcile.Result{}, err
		}
		if !found {
			r.resources.Namespaces.Delete(request.Namespace)
			log.Info("deleted namespace from resource map")
			r.resources.Services.Delete(request.NamespacedName)
			log.Info("deleted service from resource map")
		}
	}

	log.Info("reconciled udproute")

	return reconcile.Result{}, nil
}

// validateUDPRouteBackendRef validates that ref is a reference to a local Service.
func validateUDPRouteBackendRef(ref *gwapiv1a2.BackendRef) error {
	switch {
	case ref == nil:
		return nil
	case ref.Group != nil && *ref.Group != corev1.GroupName:
		return fmt.Errorf("invalid group; must be nil or empty string")
	case ref.Kind != nil && *ref.Kind != gatewayapi.KindService:
		return fmt.Errorf("invalid kind %q; must be %q",
			*ref.BackendObjectReference.Kind, gatewayapi.KindService)
	case ref.Namespace != nil:
		return fmt.Errorf("invalid namespace; must be nil")
	}

	return nil
}

// subscribeAndUpdateStatus subscribes to udproute status updates and writes it into the
// Kubernetes API Server
func (r *udpRouteReconciler) subscribeAndUpdateStatus(ctx context.Context) {
	// Subscribe to resources
	message.HandleSubscription(r.resources.UDPRouteStatuses.Subscribe(ctx),
		func(update message.Update[types.NamespacedName, *gwapiv1a2.UDPRoute]) {
			// skip delete updates.
			if update.Delete {
				return
			}
			key := update.Key
			val := update.Value
			r.statusUpdater.Send(status.Update{
				NamespacedName: key,
				Resource:       new(gwapiv1a2.UDPRoute),
				Mutator: status.MutatorFunc(func(obj client.Object) client.Object {
					t, ok := obj.(*gwapiv1a2.UDPRoute)
					if !ok {
						panic(fmt.Sprintf("unsupported object type %T", obj))
					}
					tCopy := t.DeepCopy()
					tCopy.Status.Parents = val.Status.Parents
					return tCopy
				}),
			})
		},
	)
	r.log.Info("status subscriber shutting down")
}
