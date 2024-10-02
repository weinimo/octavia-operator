/*
Copyright 2024.

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

package functional_test

import (
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"

	keystone_helpers "github.com/openstack-k8s-operators/keystone-operator/api/test/helpers"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"

	//revive:disable-next-line:dot-imports
	. "github.com/openstack-k8s-operators/lib-common/modules/common/test/helpers"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	octaviav1 "github.com/openstack-k8s-operators/octavia-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/octavia-operator/pkg/octavia"
)

var _ = Describe("Octavia controller", func() {
	var name string
	var spec map[string]interface{}
	var octaviaName types.NamespacedName
	var transportURLName types.NamespacedName
	var transportURLSecretName types.NamespacedName

	BeforeEach(func() {
		name = fmt.Sprintf("octavia-%s", uuid.New().String())
		spec = GetDefaultOctaviaSpec()

		octaviaName = types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}

		transportURLName = types.NamespacedName{
			Namespace: namespace,
			Name:      name + "-octavia-transport",
		}

		transportURLSecretName = types.NamespacedName{
			Namespace: namespace,
			Name:      RabbitmqSecretName,
		}
	})

	When("an Octavia instance is created", func() {
		BeforeEach(func() {
			DeferCleanup(th.DeleteInstance, CreateOctavia(octaviaName, spec))
		})

		It("should have the Spec fields initialized", func() {
			octavia := GetOctavia(octaviaName)
			Expect(octavia.Spec.DatabaseInstance).Should(Equal("test-octavia-db-instance"))
			Expect(octavia.Spec.Secret).Should(Equal(SecretName))
			Expect(octavia.Spec.TenantName).Should(Equal("service"))
		})

		It("should have the Status fields initialized", func() {
			octavia := GetOctavia(octaviaName)
			Expect(octavia.Status.DatabaseHostname).Should(Equal(""))
			Expect(octavia.Status.TransportURLSecret).Should(Equal(""))
			Expect(octavia.Status.OctaviaAPIReadyCount).Should(Equal(int32(0)))
			Expect(octavia.Status.OctaviaWorkerReadyCount).Should(Equal(int32(0)))
			Expect(octavia.Status.OctaviaHousekeepingReadyCount).Should(Equal(int32(0)))
			Expect(octavia.Status.OctaviaHealthManagerReadyCount).Should(Equal(int32(0)))
			Expect(octavia.Status.OctaviaRsyslogReadyCount).Should(Equal(int32(0)))
		})

		It("should have Unknown Conditions initialized as TransportUrl not created", func() {
			for _, cond := range []condition.Type{
				condition.RabbitMqTransportURLReadyCondition,
				condition.DBReadyCondition,
				condition.ServiceConfigReadyCondition,
			} {
				th.ExpectCondition(
					octaviaName,
					ConditionGetterFunc(OctaviaConditionGetter),
					cond,
					corev1.ConditionUnknown,
				)
			}
			// TODO(gthiemonge) InputReadyCondition is set to False while the controller is waiting for the transportURL to be created, this is probably not the correct behavior
			for _, cond := range []condition.Type{
				condition.InputReadyCondition,
				condition.ReadyCondition,
			} {
				th.ExpectCondition(
					octaviaName,
					ConditionGetterFunc(OctaviaConditionGetter),
					cond,
					corev1.ConditionFalse,
				)
			}
		})

		It("should have a finalizer", func() {
			// the reconciler loop adds the finalizer so we have to wait for
			// it to run
			Eventually(func() []string {
				return GetOctavia(octaviaName).Finalizers
			}, timeout, interval).Should(ContainElement("openstack.org/octavia"))
		})

		It("should not create a secret", func() {
			secret := types.NamespacedName{
				Namespace: octaviaName.Namespace,
				Name:      fmt.Sprintf("%s-%s", octaviaName.Name, "config-data"),
			}
			th.AssertSecretDoesNotExist(secret)
		})
	})

	// TransportURL
	When("a proper secret is provider, TransportURL is created", func() {
		BeforeEach(func() {
			DeferCleanup(th.DeleteInstance, CreateOctavia(octaviaName, spec))
			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))
			infra.SimulateTransportURLReady(transportURLName)
		})

		It("should be in state of having the input ready", func() {
			th.ExpectCondition(
				octaviaName,
				ConditionGetterFunc(OctaviaConditionGetter),
				condition.InputReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should be in state of having the TransportURL ready", func() {
			th.ExpectCondition(
				octaviaName,
				ConditionGetterFunc(OctaviaConditionGetter),
				condition.RabbitMqTransportURLReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should not create a secret", func() {
			secret := types.NamespacedName{
				Namespace: octaviaName.Namespace,
				Name:      fmt.Sprintf("%s-%s", octaviaName.Name, "config-data"),
			}
			th.AssertSecretDoesNotExist(secret)
		})
	})

	// Certs
	When("Certificates are created", func() {
		var keystoneAPIFixture *keystone_helpers.KeystoneAPIFixture

		BeforeEach(func() {
			keystoneAPIFixture, _, _ = SetupAPIFixtures(logger)
			keystoneName := keystone.CreateKeystoneAPIWithFixture(namespace, keystoneAPIFixture)
			DeferCleanup(keystone.DeleteKeystoneAPI, keystoneName)
			keystonePublicEndpoint := fmt.Sprintf("http://keystone-for-%s-public", octaviaName.Name)
			SimulateKeystoneReady(keystoneName, keystonePublicEndpoint, keystoneAPIFixture.Endpoint())

			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaCaPassphraseSecret(namespace, octaviaName.Name))

			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURL(transportURLName))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))
			infra.SimulateTransportURLReady(transportURLName)

			DeferCleanup(th.DeleteInstance, CreateOctavia(octaviaName, spec))
		})

		It("should set the Certs Ready Condition to true", func() {
			th.ExpectCondition(
				octaviaName,
				ConditionGetterFunc(OctaviaConditionGetter),
				octaviav1.OctaviaAmphoraCertsReadyCondition,
				corev1.ConditionTrue,
			)
		})

		FIt("creates a secret that contains PEM files", func() {
			configData := th.GetSecret(
				types.NamespacedName{
					Namespace: octaviaName.Namespace,
					Name:      fmt.Sprintf("%s-certs-secret", octaviaName.Name)})
			Expect(configData).ShouldNot(BeNil())
			expectedKeys := []string{
				"server_ca.key.pem",
				"server_ca.cert.pem",
				"client_ca.cert.pem",
				"client.cert-and-key.pem"}
			for _, filename := range expectedKeys {
				Expect(configData.Data[filename]).ShouldNot(BeEmpty())
			}
			fmt.Printf("%+v\n", string(configData.Data["server_ca.key.pem"]))
		})
	})

	// Quotas
	When("Quotas are created", func() {
		var keystoneAPIFixture *keystone_helpers.KeystoneAPIFixture
		var novaAPIFixture *NovaAPIFixture
		var neutronAPIFixture *NeutronAPIFixture

		BeforeEach(func() {
			keystoneAPIFixture, novaAPIFixture, neutronAPIFixture = SetupAPIFixtures(logger)
			keystoneName := keystone.CreateKeystoneAPIWithFixture(namespace, keystoneAPIFixture)
			DeferCleanup(keystone.DeleteKeystoneAPI, keystoneName)
			keystonePublicEndpoint := fmt.Sprintf("http://keystone-for-%s-public", octaviaName.Name)
			SimulateKeystoneReady(keystoneName, keystonePublicEndpoint, keystoneAPIFixture.Endpoint())

			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaCaPassphraseSecret(namespace, octaviaName.Name))
			SimulateOctaviaCertsSecret(namespace, octaviaName.Name)

			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURL(transportURLName))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))
			infra.SimulateTransportURLReady(transportURLName)

			DeferCleanup(th.DeleteInstance, CreateOctavia(octaviaName, spec))
		})

		It("should set the Networking and Compute Quotas", func() {
			th.ExpectCondition(
				octaviaName,
				ConditionGetterFunc(OctaviaConditionGetter),
				octaviav1.OctaviaQuotasReadyCondition,
				corev1.ConditionTrue,
			)

			instance := GetOctavia(octaviaName)
			project := GetProject(instance.Spec.TenantName)

			quotaset := novaAPIFixture.QuotaSets[project.ID]
			Expect(quotaset.RAM).To(Equal(-1))
			Expect(quotaset.Cores).To(Equal(-1))
			Expect(quotaset.Instances).To(Equal(-1))
			Expect(quotaset.ServerGroups).To(Equal(-1))
			Expect(quotaset.ServerGroupMembers).To(Equal(-1))

			quota := neutronAPIFixture.Quotas[project.ID]
			Expect(quota.Port).To(Equal(-1))
			Expect(quota.SecurityGroup).To(Equal(-1))
			Expect(quota.SecurityGroupRule).To(Equal(-1))
		})
	})

	// NAD

	// DB
	When("DB is created", func() {
		var keystoneAPIFixture *keystone_helpers.KeystoneAPIFixture

		BeforeEach(func() {
			keystoneAPIFixture, _, _ = SetupAPIFixtures(logger)
			keystoneName := keystone.CreateKeystoneAPIWithFixture(namespace, keystoneAPIFixture)
			DeferCleanup(keystone.DeleteKeystoneAPI, keystoneName)
			keystonePublicEndpoint := fmt.Sprintf("http://keystone-for-%s-public", octaviaName.Name)
			SimulateKeystoneReady(keystoneName, keystonePublicEndpoint, keystoneAPIFixture.Endpoint())

			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaCaPassphraseSecret(namespace, octaviaName.Name))
			SimulateOctaviaCertsSecret(namespace, octaviaName.Name)

			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURL(transportURLName))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))
			infra.SimulateTransportURLReady(transportURLName)

			DeferCleanup(th.DeleteInstance, CreateOctavia(octaviaName, spec))

			DeferCleanup(
				mariadb.DeleteDBService,
				mariadb.CreateDBService(
					namespace,
					GetOctavia(octaviaName).Spec.DatabaseInstance,
					corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{Port: 3306}},
					},
				),
			)
		})

		It("should set DBReady Condition and set DatabaseHostname Status", func() {
			mariadb.SimulateMariaDBAccountCompleted(types.NamespacedName{Namespace: namespace, Name: GetOctavia(octaviaName).Spec.DatabaseAccount})
			mariadb.SimulateMariaDBDatabaseCompleted(types.NamespacedName{Namespace: namespace, Name: octavia.DatabaseCRName})
			mariadb.SimulateMariaDBAccountCompleted(types.NamespacedName{Namespace: namespace, Name: GetOctavia(octaviaName).Spec.PersistenceDatabaseAccount})
			mariadb.SimulateMariaDBDatabaseCompleted(types.NamespacedName{Namespace: namespace, Name: octavia.PersistenceDatabaseCRName})
			th.SimulateJobSuccess(types.NamespacedName{Namespace: namespace, Name: octaviaName.Name + "-db-sync"})
			octavia := GetOctavia(octaviaName)
			hostname := "hostname-for-" + octavia.Spec.DatabaseInstance + "." + namespace + ".svc"
			Expect(octavia.Status.DatabaseHostname).To(Equal(hostname))
			th.ExpectCondition(
				octaviaName,
				ConditionGetterFunc(OctaviaConditionGetter),
				condition.DBReadyCondition,
				corev1.ConditionTrue,
			)
			th.ExpectCondition(
				octaviaName,
				ConditionGetterFunc(OctaviaConditionGetter),
				condition.DBSyncReadyCondition,
				corev1.ConditionFalse,
			)
		})
	})

	// Config
	When("The Config Secrets are created", func() {
		var keystoneAPIFixture *keystone_helpers.KeystoneAPIFixture

		BeforeEach(func() {
			keystoneAPIFixture, _, _ = SetupAPIFixtures(logger)
			keystoneName := keystone.CreateKeystoneAPIWithFixture(namespace, keystoneAPIFixture)
			DeferCleanup(keystone.DeleteKeystoneAPI, keystoneName)
			keystonePublicEndpoint := fmt.Sprintf("http://keystone-for-%s-public", octaviaName.Name)
			SimulateKeystoneReady(keystoneName, keystonePublicEndpoint, keystoneAPIFixture.Endpoint())

			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateOctaviaCaPassphraseSecret(namespace, octaviaName.Name))
			SimulateOctaviaCertsSecret(namespace, octaviaName.Name)

			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURL(transportURLName))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))
			infra.SimulateTransportURLReady(transportURLName)

			DeferCleanup(th.DeleteInstance, CreateOctavia(octaviaName, spec))

			DeferCleanup(
				mariadb.DeleteDBService,
				mariadb.CreateDBService(
					namespace,
					GetOctavia(octaviaName).Spec.DatabaseInstance,
					corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{Port: 3306}},
					},
				),
			)

			mariadb.SimulateMariaDBAccountCompleted(types.NamespacedName{Namespace: namespace, Name: GetOctavia(octaviaName).Spec.DatabaseAccount})
			mariadb.SimulateMariaDBDatabaseCompleted(types.NamespacedName{Namespace: namespace, Name: octavia.DatabaseCRName})
			mariadb.SimulateMariaDBAccountCompleted(types.NamespacedName{Namespace: namespace, Name: GetOctavia(octaviaName).Spec.PersistenceDatabaseAccount})
			mariadb.SimulateMariaDBDatabaseCompleted(types.NamespacedName{Namespace: namespace, Name: octavia.PersistenceDatabaseCRName})
			th.SimulateJobSuccess(types.NamespacedName{Namespace: namespace, Name: octaviaName.Name + "-db-sync"})
		})

		It("should set Service Config Ready Condition", func() {
			th.ExpectCondition(
				octaviaName,
				ConditionGetterFunc(OctaviaConditionGetter),
				condition.ServiceConfigReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should create the octavia.conf file in a Secret", func() {
			instance := GetOctavia(octaviaName)

			configData := th.GetSecret(
				types.NamespacedName{
					Namespace: octaviaName.Namespace,
					Name:      fmt.Sprintf("%s-config-data", octaviaName.Name)})
			Expect(configData).ShouldNot(BeNil())
			conf := string(configData.Data["octavia.conf"])
			Expect(conf).Should(
				ContainSubstring(
					fmt.Sprintf(
						"username=%s\n",
						instance.Spec.ServiceUser)))

			dbs := []struct {
				Name            string
				DatabaseAccount string
				Keyword         string
			}{
				{
					Name:            octavia.DatabaseName,
					DatabaseAccount: instance.Spec.DatabaseAccount,
					Keyword:         "connection",
				}, {
					Name:            octavia.PersistenceDatabaseName,
					DatabaseAccount: instance.Spec.PersistenceDatabaseAccount,
					Keyword:         "persistence_connection",
				},
			}

			for _, db := range dbs {
				databaseAccount := mariadb.GetMariaDBAccount(
					types.NamespacedName{
						Namespace: namespace,
						Name:      db.DatabaseAccount})
				databaseSecret := th.GetSecret(
					types.NamespacedName{
						Namespace: namespace,
						Name:      databaseAccount.Spec.Secret})

				Expect(conf).Should(
					ContainSubstring(
						fmt.Sprintf(
							"%s = mysql+pymysql://%s:%s@%s/%s?read_default_file=/etc/my.cnf",
							db.Keyword,
							databaseAccount.Spec.UserName,
							databaseSecret.Data[mariadbv1.DatabasePasswordSelector],
							instance.Status.DatabaseHostname,
							db.Name)))
			}
		})

		It("should create a Secret for the scripts", func() {
			scriptData := th.GetSecret(
				types.NamespacedName{
					Namespace: octaviaName.Namespace,
					Name:      fmt.Sprintf("%s-scripts", octaviaName.Name)})
			Expect(scriptData).ShouldNot(BeNil())
		})
	})

	// Create Networks Annotation

	// API Deployment

	// Network Management

	// Predictable IPs

	// Amphora Controller Daemonsets

	// Rsyslog Daemonset

	// Amp SSH Config

	// Amphora Image
})
