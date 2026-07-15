package provider

import (
	"fmt"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	barman "github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"

	barmancloudv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

// Compile-time check that Provider implements the required interface.
var _ controller.ProviderInterface = (*Provider)(nil)

// Provider implements controller.ProviderInterface for the provider-cloudnative-pg provider.
type Provider struct {
	controller.BaseProvider
}

// New creates a new Provider instance.
func New() *Provider {
	return &Provider{
		BaseProvider: controller.BaseProvider{
			ProviderName: common.ProviderName,
			WatchConfigs: []controller.WatchConfig{
				controller.WatchOwned(&cnpgv1.Cluster{}),
				controller.WatchOwned(&barmancloudv1.ObjectStore{}),
			},
			SchemeFuncs: []func(*runtime.Scheme) error{
				cnpgv1.SchemeBuilder.AddToScheme,
				addBarmanCloudScheme,
			},
		},
	}
}

func addBarmanCloudScheme(scheme *runtime.Scheme) error {
	barmancloudv1.AddKnownTypes(scheme)
	return nil
}

// Validate checks if the Instance spec is valid.
//
// Add your provider-specific validation logic here.
// Return an error if the spec is invalid.
//
// +kubebuilder:rbac:groups=<operator-api-group>,resources=<operator-resources>,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=<operator-api-group>,resources=<operator-resources>/status,verbs=get
func (p *Provider) Validate(c *controller.Context) error {
	l := log.FromContext(c.Context())
	l.Info("Validating instance", "name", c.Name())

	engine, ok := c.Instance().Spec.Components[common.ComponentEngine]
	if !ok {
		return fmt.Errorf("engine component is required")
	}

	if err := cnpg.ValidateEngine(engine); err != nil {
		return err
	}

	var custom components.PostgresqlCustomSpec
	if c.TryDecodeComponentCustomSpec(engine, &custom) {
		if err := c.DecodeComponentCustomSpec(engine, &custom); err != nil {
			return fmt.Errorf("failed to decode component custom spec: %w", err)
		}
	}

	return cnpg.ValidateCustomSpec(&custom)
}

// Sync ensures all required resources exist and are configured correctly.
//
// This is the main reconciliation logic. Create or update your operator
// operator's custom resource(s) based on the Instance spec.
func (p *Provider) Sync(c *controller.Context) error {
	l := log.FromContext(c.Context())
	l.Info("Syncing instance", "name", c.Name())

	engine := c.Instance().Spec.Components[common.ComponentEngine]

	var custom components.PostgresqlCustomSpec
	if c.TryDecodeComponentCustomSpec(engine, &custom) {
		if err := c.DecodeComponentCustomSpec(engine, &custom); err != nil {
			return fmt.Errorf("failed to decode component custom spec: %w", err)
		}
	}

	pg := &cnpgv1.Cluster{
		ObjectMeta: c.ObjectMeta(c.Name()),
		Spec:       buildClusterSpec(engine, custom),
	}

	if custom.PostgresConfiguration != nil {
		pg.Spec.PostgresConfiguration = *custom.PostgresConfiguration
	}

	if custom.Managed != nil {
		pg.Spec.Managed = custom.Managed
	}

	if custom.Affinity != nil {
		pg.Spec.Affinity = *custom.Affinity
	}

	if custom.Bootstrap != nil {
		pg.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{
			InitDB: custom.Bootstrap.InitDB,
		}
	}

	if engine.Image != "" {
		// User explicitly specified an image override.
		pg.Spec.ImageName = engine.Image
	} else {
		spec, err := c.ProviderSpec()
		if err != nil {
			return err
		}
		if engine.Version != "" {
			pg.Spec.ImageName = controller.GetImageForVersion(spec, common.ComponentEngine, engine.Version)
		}
		if pg.Spec.ImageName == "" {
			pg.Spec.ImageName = controller.GetDefaultImageForComponent(spec, common.ComponentEngine)
		}
	}

	if pg.Spec.ImagePullPolicy == "" {
		pg.Spec.ImagePullPolicy = corev1.PullIfNotPresent
	}

	if custom.Certificates != nil {
		pg.Spec.Certificates = custom.Certificates
	}

	if custom.Monitoring != nil {
		pg.Spec.Monitoring = custom.Monitoring
	}

	plugins, err := barman.SyncBackupInfrastructure(c)
	if err != nil {
		return err
	}
	if len(plugins) > 0 {
		pg.Spec.Plugins = plugins
	}

	if err := c.Apply(pg); err != nil {
		return err
	}
	return nil
}

func buildClusterSpec(engine corev1alpha1.ComponentSpec, custom components.PostgresqlCustomSpec) cnpgv1.ClusterSpec {
	storage := cnpgv1.StorageConfiguration{
		Size: engine.Storage.Size.String(),
	}

	if engine.Storage.StorageClass != nil && *engine.Storage.StorageClass != "" {
		storage.StorageClass = engine.Storage.StorageClass
	}

	if custom.PersistentVolumeClaimTemplate != nil {
		storage.PersistentVolumeClaimTemplate = custom.PersistentVolumeClaimTemplate
	}

	if custom.ResizeInUseVolumes != nil {
		storage.ResizeInUseVolumes = custom.ResizeInUseVolumes
	}

	spec := cnpgv1.ClusterSpec{
		Instances:            int(*engine.Replicas),
		StorageConfiguration: storage,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    *engine.Resources.Requests.Cpu(),
				corev1.ResourceMemory: *engine.Resources.Requests.Memory(),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    *engine.Resources.Limits.Cpu(),
				corev1.ResourceMemory: *engine.Resources.Limits.Memory(),
			},
		},
	}

	return spec
}

// Status computes the current status of the database instance.
//
// Query the operator's resource(s) and translate their status
// into the provider-runtime's Status type.
func (p *Provider) Status(c *controller.Context) (controller.Status, error) {
	l := log.FromContext(c.Context())
	l.Info("Computing status", "name", c.Name())

	pg := &cnpgv1.Cluster{}
	if err := c.Get(pg, c.Name()); err != nil {
		return controller.Pending("Waiting to get CloudNativePG cluster resource"), nil
	}

	readyCondition := meta.FindStatusCondition(pg.Status.Conditions, string(cnpgv1.ConditionClusterReady))
	if readyCondition != nil && readyCondition.Status == metav1.ConditionTrue && pg.Status.Instances > 0 && pg.Status.ReadyInstances == pg.Status.Instances {
		if roleStatus, blocked := cnpg.ManagedRolesStatus(pg); blocked {
			return roleStatus, nil
		}

		host := fmt.Sprintf("%s.%s.svc", pg.GetServiceReadWriteName(), c.Namespace())

		var (
			secretName string
			username   string
			password   string
		)
		for _, candidateSecretName := range []string{pg.GetApplicationSecretName(), pg.GetSuperuserSecretName()} {
			secret := &corev1.Secret{}
			if err := c.Get(secret, candidateSecretName); err != nil {
				continue
			}
			u := string(secret.Data["username"])
			p := string(secret.Data["password"])
			if u != "" && p != "" {
				secretName = candidateSecretName
				username = u
				password = p
				break
			}
		}

		if username == "" || password == "" {
			return controller.Provisioning("waiting to get CloudNativePG credentials secret"), nil
		}

		database := pg.GetApplicationDatabaseName()
		if database == "" {
			database = "postgres"
		}
		uri := fmt.Sprintf(
			"postgresql://%s:%s@%s:%s/%s",
			url.QueryEscape(username),
			url.QueryEscape(password),
			host,
			"5432",
			url.PathEscape(database),
		)

		return controller.ReadyWithConnectionDetails(controller.ConnectionDetails{
			Type:     "postgresql",
			Provider: common.ProviderName,
			Host:     host,
			Port:     "5432",
			Username: username,
			Password: password,
			URI:      uri,
			AdditionalProperties: map[string]string{
				"database":   database,
				"secretName": secretName,
			},
		}), nil
	}

	if pg.Status.Instances > 0 {
		return controller.Provisioning(fmt.Sprintf(
			"waiting for CloudNativePG cluster to be ready (%d/%d instances ready)",
			pg.Status.ReadyInstances,
			pg.Status.Instances,
		)), nil
	} else {
		return controller.Initializing("waiting for CloudNativePG cluster to initialize"), nil
	}
}

// Cleanup handles deletion of provider-managed resources.
//
// Called when the Instance has a deletion timestamp set.
// Delete any resources that are not automatically cleaned up
// via owner references.
func (p *Provider) Cleanup(c *controller.Context) error {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up instance", "name", c.Name())

	// TODO: Implement cleanup logic if needed.
	// Resources with owner references set via c.Apply() are automatically
	// garbage collected. Only implement this if you need custom cleanup.
	return nil
}
