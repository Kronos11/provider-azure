/*
Copyright 2018 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package compute

import (
	"context"
	"log"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/containerservice/mgmt/2018-03-31/containerservice"
	"github.com/Azure/azure-sdk-for-go/services/graphrbac/1.6/graphrbac"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	computev1alpha1 "github.com/crossplaneio/crossplane/pkg/apis/azure/compute/v1alpha1"
	"github.com/crossplaneio/crossplane/pkg/apis/azure/v1alpha1"
	corev1alpha1 "github.com/crossplaneio/crossplane/pkg/apis/core/v1alpha1"
	azureclients "github.com/crossplaneio/crossplane/pkg/clients/azure"
	"github.com/crossplaneio/crossplane/pkg/test"
)

type mockAKSSetupClientFactory struct {
	mockClient *mockAKSSetupClient
}

func (m *mockAKSSetupClientFactory) CreateSetupClient(*v1alpha1.Provider, kubernetes.Interface) (*azureclients.AKSSetupClient, error) {
	return &azureclients.AKSSetupClient{
		AKSClusterAPI:       m.mockClient,
		ApplicationAPI:      m.mockClient,
		ServicePrincipalAPI: m.mockClient,
	}, nil
}

type mockAKSSetupClient struct {
	MockGet                         func(ctx context.Context, instance computev1alpha1.AKSCluster) (containerservice.ManagedCluster, error)
	MockCreateOrUpdateBegin         func(ctx context.Context, instance computev1alpha1.AKSCluster, clusterName, appID, spSecret string) ([]byte, error)
	MockCreateOrUpdateEnd           func(op []byte) (bool, error)
	MockDelete                      func(ctx context.Context, instance computev1alpha1.AKSCluster) (containerservice.ManagedClustersDeleteFuture, error)
	MockListClusterAdminCredentials func(ctx context.Context, instance computev1alpha1.AKSCluster) (containerservice.CredentialResults, error)
	MockCreateApplication           func(ctx context.Context, appParams azureclients.ApplicationParameters) (*graphrbac.Application, error)
	MockDeleteApplication           func(ctx context.Context, appObjectID string) error
	MockCreateServicePrincipal      func(ctx context.Context, spID, appID string) (*graphrbac.ServicePrincipal, error)
	MockDeleteServicePrincipal      func(ctx context.Context, spID string) error
}

func (m *mockAKSSetupClient) Get(ctx context.Context, instance computev1alpha1.AKSCluster) (containerservice.ManagedCluster, error) {
	if m.MockGet != nil {
		return m.MockGet(ctx, instance)
	}
	return containerservice.ManagedCluster{}, nil
}

func (m *mockAKSSetupClient) CreateOrUpdateBegin(ctx context.Context, instance computev1alpha1.AKSCluster, clusterName, appID, spSecret string) ([]byte, error) {
	if m.MockCreateOrUpdateBegin != nil {
		return m.MockCreateOrUpdateBegin(ctx, instance, clusterName, appID, spSecret)
	}
	return nil, nil
}

func (m *mockAKSSetupClient) CreateOrUpdateEnd(op []byte) (bool, error) {
	if m.MockCreateOrUpdateEnd != nil {
		return m.MockCreateOrUpdateEnd(op)
	}
	return true, nil
}

func (m *mockAKSSetupClient) Delete(ctx context.Context, instance computev1alpha1.AKSCluster) (containerservice.ManagedClustersDeleteFuture, error) {
	if m.MockDelete != nil {
		return m.MockDelete(ctx, instance)
	}
	return containerservice.ManagedClustersDeleteFuture{}, nil
}

func (m *mockAKSSetupClient) ListClusterAdminCredentials(ctx context.Context, instance computev1alpha1.AKSCluster) (containerservice.CredentialResults, error) {
	if m.MockListClusterAdminCredentials != nil {
		return m.MockListClusterAdminCredentials(ctx, instance)
	}
	return containerservice.CredentialResults{}, nil
}

func (m *mockAKSSetupClient) CreateApplication(ctx context.Context, appParams azureclients.ApplicationParameters) (*graphrbac.Application, error) {
	if m.MockCreateApplication != nil {
		return m.MockCreateApplication(ctx, appParams)
	}
	return nil, nil
}

func (m *mockAKSSetupClient) DeleteApplication(ctx context.Context, appObjectID string) error {
	if m.MockDeleteApplication != nil {
		return m.MockDeleteApplication(ctx, appObjectID)
	}
	return nil
}

func (m *mockAKSSetupClient) CreateServicePrincipal(ctx context.Context, spID, appID string) (*graphrbac.ServicePrincipal, error) {
	if m.MockCreateServicePrincipal != nil {
		return m.MockCreateServicePrincipal(ctx, spID, appID)
	}
	return nil, nil
}

func (m *mockAKSSetupClient) DeleteServicePrincipal(ctx context.Context, spID string) error {
	if m.MockDeleteServicePrincipal != nil {
		return m.MockDeleteServicePrincipal(ctx, spID)
	}
	return nil
}

func TestReconcile(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	clientset := fake.NewSimpleClientset()
	mockAKSSetupClient := &mockAKSSetupClient{}
	mockAKSSetupClientFactory := &mockAKSSetupClientFactory{mockClient: mockAKSSetupClient}

	// setup all the mocked functions for the AKS setup client
	mockAKSSetupClient.MockCreateApplication = func(ctx context.Context, appParams azureclients.ApplicationParameters) (*graphrbac.Application, error) {
		return &graphrbac.Application{
			ObjectID: to.StringPtr("182f8c4a-ad89-4b25-b947-d4026ab183a1"),
			AppID:    to.StringPtr("e163d435-00d2-4ea8-9735-b875990e453e"),
		}, nil
	}
	mockAKSSetupClient.MockCreateServicePrincipal = func(ctx context.Context, spID, appID string) (*graphrbac.ServicePrincipal, error) {
		return &graphrbac.ServicePrincipal{
			ObjectID: to.StringPtr("da804153-3faa-4c73-9fcb-0961387a31f9"),
		}, nil
	}
	mockAKSSetupClient.MockCreateOrUpdateBegin = func(ctx context.Context, instance computev1alpha1.AKSCluster, clusterName, appID, spSecret string) ([]byte, error) {
		return []byte("mocked marshalled create future"), nil
	}
	mockAKSSetupClient.MockGet = func(ctx context.Context, instance computev1alpha1.AKSCluster) (containerservice.ManagedCluster, error) {
		return containerservice.ManagedCluster{
			ID: to.StringPtr("fcb4e97a-c3ea-4466-9b02-e728d8e6764f"),
			ManagedClusterProperties: &containerservice.ManagedClusterProperties{
				ProvisioningState: to.StringPtr("Succeeded"),
				Fqdn:              to.StringPtr("crossplane-aks.foo.azure.com"),
			},
		}, nil
	}
	mockAKSSetupClient.MockListClusterAdminCredentials = func(ctx context.Context, instance computev1alpha1.AKSCluster) (containerservice.CredentialResults, error) {
		return containerservice.CredentialResults{
			Kubeconfigs: &[]containerservice.CredentialResult{{Value: &kubecfg}},
		}, nil
	}

	// Setup the Manager and Controller.  Wrap the Controller Reconcile function so it writes each request to a
	// channel when it is finished.
	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c := mgr.GetClient()

	r := newAKSClusterReconciler(mgr, mockAKSSetupClientFactory, clientset)
	recFn, requests := SetupTestReconcile(r)
	g.Expect(AddAKSClusterReconciler(mgr, recFn)).NotTo(gomega.HaveOccurred())
	defer close(StartTestManager(mgr, g))

	// create the provider object and defer its cleanup
	provider := testProvider(testSecret([]byte("testdata")))
	provider.Status.UnsetAllConditions()
	provider.Status.SetReady()
	err = c.Create(ctx, provider)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	defer c.Delete(ctx, provider)

	// Create the AKS cluster object and defer its clean up
	instance := testInstance(provider)
	err = c.Create(ctx, instance)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	defer cleanupAKSCluster(g, c, requests, instance)

	// first reconcile loop should start the create operation
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))

	// after the first reconcile, the create operation should be saved on the running operation field,
	// and the following should be set:
	// 1) cluster name
	// 2) application object ID
	// 3) service principal ID
	// 4) "creating" condition
	expectedStatus := computev1alpha1.AKSClusterStatus{
		RunningOperation:    "mocked marshalled create future",
		ClusterName:         instanceName,
		ApplicationObjectID: "182f8c4a-ad89-4b25-b947-d4026ab183a1",
		ServicePrincipalID:  "da804153-3faa-4c73-9fcb-0961387a31f9",
		ConditionedStatus: corev1alpha1.ConditionedStatus{
			Conditions: []corev1alpha1.Condition{
				{
					Type:   corev1alpha1.Creating,
					Status: v1.ConditionTrue,
				},
			},
		},
	}
	assertAKSClusterStatus(g, c, expectedStatus)

	// the service principal secret (note this is not the connection secret) should have been created
	spSecret, err := r.clientset.CoreV1().Secrets(namespace).Get("test-compute-instance-service-principal", metav1.GetOptions{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	spSecretValue, ok := spSecret.Data[spSecretKey]
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(spSecretValue).ToNot(gomega.BeEmpty())

	// second reconcile should finish the create operation and clear out the running operation field
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))
	expectedStatus = computev1alpha1.AKSClusterStatus{
		RunningOperation:    "",
		ClusterName:         instanceName,
		ApplicationObjectID: "182f8c4a-ad89-4b25-b947-d4026ab183a1",
		ServicePrincipalID:  "da804153-3faa-4c73-9fcb-0961387a31f9",
		ConditionedStatus: corev1alpha1.ConditionedStatus{
			Conditions: []corev1alpha1.Condition{
				{
					Type:   corev1alpha1.Creating,
					Status: v1.ConditionTrue,
				},
			},
		},
	}
	assertAKSClusterStatus(g, c, expectedStatus)

	// third reconcile should find the AKS cluster instance from Azure and update the full status of the CRD
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))

	// verify that the CRD status was updated with details about the external AKS cluster and that the
	// CRD conditions show the transition from creating to running
	expectedStatus = computev1alpha1.AKSClusterStatus{
		ClusterName:         instanceName,
		State:               "Succeeded",
		ProviderID:          "fcb4e97a-c3ea-4466-9b02-e728d8e6764f",
		Endpoint:            "crossplane-aks.foo.azure.com",
		ApplicationObjectID: "182f8c4a-ad89-4b25-b947-d4026ab183a1",
		ServicePrincipalID:  "da804153-3faa-4c73-9fcb-0961387a31f9",
		ConditionedStatus: corev1alpha1.ConditionedStatus{
			Conditions: []corev1alpha1.Condition{
				{
					Type:   corev1alpha1.Creating,
					Status: v1.ConditionFalse,
				},
				{
					Type:   corev1alpha1.Ready,
					Status: v1.ConditionTrue,
				},
			},
		},
	}
	assertAKSClusterStatus(g, c, expectedStatus)

	// wait for the connection information to be stored in a secret, then verify it
	var connectionSecret *v1.Secret
	for {
		if connectionSecret, err = r.clientset.CoreV1().Secrets(namespace).Get(instanceName, metav1.GetOptions{}); err == nil {
			if string(connectionSecret.Data[corev1alpha1.ResourceCredentialsSecretEndpointKey]) != "" {
				break
			}
		}
	}
	assertConnectionSecret(g, c, connectionSecret)

	// verify that a finalizer was added to the CRD
	c.Get(ctx, expectedRequest.NamespacedName, instance)
	g.Expect(len(instance.Finalizers)).To(gomega.Equal(1))
	g.Expect(instance.Finalizers[0]).To(gomega.Equal(finalizer))

	// test deletion of the instance
	cleanupAKSCluster(g, c, requests, instance)
}

func cleanupAKSCluster(g *gomega.GomegaWithT, c client.Client, requests chan reconcile.Request, instance *computev1alpha1.AKSCluster) {
	deletedInstance := &computev1alpha1.AKSCluster{}
	if err := c.Get(ctx, expectedRequest.NamespacedName, deletedInstance); errors.IsNotFound(err) {
		// instance has already been deleted, bail out
		return
	}

	log.Printf("cleaning up AKS cluster instance %s by deleting the CRD", instance.Name)
	err := c.Delete(ctx, instance)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	// wait for the deletion timestamp to be set
	err = wait.ExponentialBackoff(test.DefaultRetry, func() (done bool, err error) {
		deletedInstance := &computev1alpha1.AKSCluster{}
		c.Get(ctx, expectedRequest.NamespacedName, deletedInstance)
		if deletedInstance.DeletionTimestamp != nil {
			return true, nil
		}
		return false, nil
	})
	g.Expect(err).NotTo(gomega.HaveOccurred())

	// wait for the reconcile to happen that handles the CRD deletion
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))

	// wait for the finalizer to run and the instance to be deleted for good
	err = wait.ExponentialBackoff(test.DefaultRetry, func() (done bool, err error) {
		deletedInstance := &computev1alpha1.AKSCluster{}
		if err := c.Get(ctx, expectedRequest.NamespacedName, deletedInstance); errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	g.Expect(err).NotTo(gomega.HaveOccurred())
}

func assertAKSClusterStatus(g *gomega.GomegaWithT, c client.Client, expectedStatus computev1alpha1.AKSClusterStatus) {
	instance := &computev1alpha1.AKSCluster{}
	err := c.Get(ctx, expectedRequest.NamespacedName, instance)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	// assert the expected status properties
	g.Expect(instance.Status.ClusterName).To(gomega.HavePrefix(expectedStatus.ClusterName))
	g.Expect(instance.Status.State).To(gomega.Equal(expectedStatus.State))
	g.Expect(instance.Status.ProviderID).To(gomega.Equal(expectedStatus.ProviderID))
	g.Expect(instance.Status.Endpoint).To(gomega.Equal(expectedStatus.Endpoint))
	g.Expect(instance.Status.ApplicationObjectID).To(gomega.Equal(expectedStatus.ApplicationObjectID))
	g.Expect(instance.Status.ServicePrincipalID).To(gomega.Equal(expectedStatus.ServicePrincipalID))
	g.Expect(instance.Status.RunningOperation).To(gomega.Equal(expectedStatus.RunningOperation))

	// assert the expected status conditions
	corev1alpha1.AssertConditions(g, expectedStatus.Conditions, instance.Status.ConditionedStatus)
}

func assertConnectionSecret(g *gomega.GomegaWithT, c client.Client, connectionSecret *v1.Secret) {
	instance := &computev1alpha1.AKSCluster{}
	err := c.Get(ctx, expectedRequest.NamespacedName, instance)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(connectionSecret.Data).Should(gomega.Equal(map[string][]byte{
		corev1alpha1.ResourceCredentialsSecretEndpointKey:   []byte(clientEndpoint),
		corev1alpha1.ResourceCredentialsSecretCAKey:         []byte(clientCAdata),
		corev1alpha1.ResourceCredentialsSecretClientCertKey: []byte(clientCert),
		corev1alpha1.ResourceCredentialsSecretClientKeyKey:  []byte(clientKey),
	}))
}
