package kubernetes

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
)

func resourceKubernetesCustom() *schema.Resource {
	return &schema.Resource{
		Create: resourceKubernetesCustomCreate,
		Read:   resourceKubernetesCustomRead,
		Update: resourceKubernetesCustomUpdate,
		Delete: resourceKubernetesCustomDelete,
		Exists: resourceKubernetesCustomExists,
		// FIXME
		// Importer: &schema.ResourceImporter{
		// 	State: schema.ImportStatePassthrough,
		// },
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(5 * time.Minute),
			Delete: schema.DefaultTimeout(5 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"json": {
				Type:        schema.TypeString,
				Description: "The raw JSON for a kubernetes API resource.",
				Required:    true,

				DiffSuppressFunc: func(k, oldJSON, newJSON string, d *schema.ResourceData) bool {
					// FIXME handle errors
					old, _ := decodeJSONToUnstructured(oldJSON)
					new, _ := decodeJSONToUnstructured(newJSON)

					if reflect.DeepEqual(old, new) {
						return true
					}

					return false
				},
			},
		},
	}
}

func resourceKubernetesCustomCreate(d *schema.ResourceData, m interface{}) error {
	config := d.Get("json").(string)
	u, _ := decodeJSONToUnstructured(config)

	clientset := m.(*KubeClientsets).MainClientset
	dclient := m.(*KubeClientsets).DynamicClient
	resource, namespace, err := createResourceInterfaceFromUnstructured(u, clientset, dclient)

	if err != nil {
		return fmt.Errorf("Could not determine resource type: %v", err)
	}

	name := u.GetName()
	id := name

	if namespace != "" {
		id = fmt.Sprintf("%s/%s", namespace, name)
	}

	_, err = resource.Create(u, metav1.CreateOptions{})

	if err != nil {
		return fmt.Errorf("Could not create resource: %v", err)
	}

	d.SetId(id)

	return resourceKubernetesCustomRead(d, m)
}

func resourceKubernetesCustomRead(d *schema.ResourceData, m interface{}) error {
	config := d.Get("json").(string)
	u, _ := decodeJSONToUnstructured(config)

	clientset := m.(*KubeClientsets).MainClientset
	dclient := m.(*KubeClientsets).DynamicClient

	resource, _, _ := createResourceInterfaceFromUnstructured(u, clientset, dclient)
	name := u.GetName()

	res, err := resource.Get(name, metav1.GetOptions{})

	if err != nil {
		return fmt.Errorf("Could not get resource: %v", err)
	}

	removeIgnoredFields(res)

	_, namespaceSet, _ := unstructured.NestedString(u.Object, "metadata", "namespace")

	if !namespaceSet {
		unstructured.RemoveNestedField(res.Object, "metadata", "namespace")
	}

	rawJSON, err := res.MarshalJSON()

	d.Set("json", string(rawJSON))

	return nil
}

func resourceKubernetesCustomUpdate(d *schema.ResourceData, m interface{}) error {
	if d.HasChange("json") {
		config := d.Get("json").(string)
		u, _ := decodeJSONToUnstructured(config)

		clientset := m.(*KubeClientsets).MainClientset
		dclient := m.(*KubeClientsets).DynamicClient
		resource, _, _ := createResourceInterfaceFromUnstructured(u, clientset, dclient)
		name := u.GetName()

		res, err := resource.Get(name, metav1.GetOptions{})

		resourceVersion := res.GetResourceVersion()
		u.SetResourceVersion(resourceVersion)

		_, err = resource.Update(u, metav1.UpdateOptions{})

		if err != nil {
			return fmt.Errorf("Could not update resource: %v", err)
		}
	}

	return resourceKubernetesCustomRead(d, m)
}

func resourceKubernetesCustomDelete(d *schema.ResourceData, m interface{}) error {
	config := d.Get("json").(string)
	u, _ := decodeJSONToUnstructured(config)

	clientset := m.(*KubeClientsets).MainClientset
	dclient := m.(*KubeClientsets).DynamicClient

	resource, _, _ := createResourceInterfaceFromUnstructured(u, clientset, dclient)
	name := u.GetName()

	err := resource.Delete(name, &metav1.DeleteOptions{})

	if err != nil {
		return fmt.Errorf("Could not delete resource: %v", err)
	}

	return nil
}

func resourceKubernetesCustomExists(d *schema.ResourceData, m interface{}) (bool, error) {
	config := d.Get("json").(string)
	u, _ := decodeJSONToUnstructured(config)

	clientset := m.(*KubeClientsets).MainClientset
	dclient := m.(*KubeClientsets).DynamicClient

	resource, _, _ := createResourceInterfaceFromUnstructured(u, clientset, dclient)
	name := u.GetName()

	_, err := resource.Get(name, metav1.GetOptions{})

	if err != nil {
		// FIXME only return false if error is not found
		return false, nil
	}

	return true, nil
}

var ignoredFields = [][]string{
	[]string{"metadata", "creationTimestamp"},
	[]string{"metadata", "resourceVersion"},
	[]string{"metadata", "uid"},
	[]string{"metadata", "selfLink"},
	[]string{"metadata", "generation"},
	[]string{"status"},
}

func removeIgnoredFields(u *unstructured.Unstructured) {
	for _, field := range ignoredFields {
		unstructured.RemoveNestedField(u.Object, field...)
	}
}

// decodeJSONToUnstructured will parse a JSON string into an Unstructured
func decodeJSONToUnstructured(config string) (*unstructured.Unstructured, error) {
	var m map[string]interface{}

	err := json.Unmarshal([]byte(config), &m)

	if err != nil {
		return nil, err
	}

	var u = unstructured.Unstructured{
		Object: m,
	}

	removeIgnoredFields(&u)

	return &u, nil
}

func getNamespaceOrDefault(u *unstructured.Unstructured) string {
	n := u.GetNamespace()

	if n == "" {
		return "default"
	}

	return n
}

func createResourceInterfaceFromUnstructured(r *unstructured.Unstructured, clientset *kubernetes.Clientset, dclient dynamic.Interface) (dynamic.ResourceInterface, string, error) {
	// figure out the REST mapping for the resource
	d := clientset.Discovery()
	groupResources, err := restmapper.GetAPIGroupResources(d)

	if err != nil {
		return nil, "", err
	}

	gvk := r.GroupVersionKind()
	gk := gvk.GroupKind()

	rm := restmapper.NewDiscoveryRESTMapper(groupResources)
	mapping, err := rm.RESTMapping(gk, gvk.Version)

	// figure out if the Resource is namespaced
	gv := r.GroupVersionKind().GroupVersion()
	apiResources, err := d.ServerResourcesForGroupVersion(gv.String())

	if err != nil {
		return nil, "", err
	}

	var namespaced bool
	for _, rl := range apiResources.APIResources {
		if rl.Kind == gk.Kind {
			namespaced = rl.Namespaced
			break
		}
	}

	if namespaced {
		namespace := getNamespaceOrDefault(r)
		return dclient.Resource(mapping.Resource).Namespace(namespace), namespace, nil
	}

	return dclient.Resource(mapping.Resource), "", nil
}
