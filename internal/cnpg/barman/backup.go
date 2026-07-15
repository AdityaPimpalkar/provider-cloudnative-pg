package barman

import (
	"fmt"
	"strings"

	"github.com/AlekSi/pointer"
	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	barmancloudv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func SyncBackupInfrastructure(c *controller.Context) ([]cnpgv1.PluginConfiguration, error) {
	backupCfg := c.Instance().Spec.Backup
	if backupCfg == nil || !backupCfg.Enabled || len(backupCfg.Storages) == 0 {
		return nil, nil
	}

	mainName := selectMainStorageName(backupCfg.Storages)
	for _, strg := range backupCfg.Storages {
		bg, err := c.BackupStorage(strg.StorageRef.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, controller.WaitFor(fmt.Sprintf(
					"BackupStorage %q not yet present", strg.StorageRef.Name))
			}
			return nil, &controller.BackupConfigError{
				Reason:  "StorageResolutionFailed",
				Message: err.Error(),
			}
		}
		if bg.Spec.S3 == nil {
			return nil, &controller.BackupConfigError{
				Reason:  "UnsupportedStorage",
				Message: fmt.Sprintf("BackupStorage %q is not S3", bg.Name),
			}
		}

		accessKeyID, secretAccessKey, err := c.BackupStorageCredentials(bg)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, controller.WaitFor(fmt.Sprintf(
					"credentials Secret %q for BackupStorage %q not yet present",
					bg.Spec.S3.CredentialsSecretName, bg.Name))
			}
			return nil, &controller.BackupConfigError{
				Reason:  "CredentialsUnavailable",
				Message: err.Error(),
			}
		}
		if accessKeyID == "" || secretAccessKey == "" {
			return nil, &controller.BackupConfigError{
				Reason: "CredentialsIncomplete",
				Message: fmt.Sprintf(
					"Secret %q must contain AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY",
					bg.Spec.S3.CredentialsSecretName),
			}
		}

		endpointCA, err := endpointCARef(c, strg.Name, bg.Spec.S3.EndpointURL)
		if err != nil {
			return nil, err
		}

		objStore := buildObjectStore(c, strg.Name, bg, endpointCA)
		if err := c.Apply(objStore); err != nil {
			return nil, fmt.Errorf("apply ObjectStore %q: %w", strg.Name, err)
		}
	}

	return []cnpgv1.PluginConfiguration{{
		Name:          PluginName,
		Enabled:       pointer.To(true),
		IsWALArchiver: pointer.To(true),
		Parameters: map[string]string{
			PluginParameterObjectStore: mainName,
		},
	}}, nil
}

func buildObjectStore(
	c *controller.Context,
	logicalName string,
	bg *backupv1alpha1.BackupStorage,
	endpointCA *machineryapi.SecretKeySelector,
) *barmancloudv1.ObjectStore {
	s3 := bg.Spec.S3
	secretName := s3.CredentialsSecretName

	return &barmancloudv1.ObjectStore{
		ObjectMeta: c.ObjectMeta(logicalName),
		Spec: barmancloudv1.ObjectStoreSpec{
			Configuration: barmanapi.BarmanObjectStoreConfiguration{
				EndpointURL:     s3.EndpointURL,
				EndpointCA:      endpointCA,
				DestinationPath: fmt.Sprintf("s3://%s/", s3.Bucket),
				BarmanCredentials: barmanapi.BarmanCredentials{
					AWS: &barmanapi.S3Credentials{
						AccessKeyIDReference: &cnpgv1.SecretKeySelector{
							LocalObjectReference: cnpgv1.LocalObjectReference{Name: secretName},
							Key:                  "AWS_ACCESS_KEY_ID",
						},
						SecretAccessKeyReference: &cnpgv1.SecretKeySelector{
							LocalObjectReference: cnpgv1.LocalObjectReference{Name: secretName},
							Key:                  "AWS_SECRET_ACCESS_KEY",
						},
					},
				},
				Wal: &barmanapi.WalBackupConfiguration{
					Compression: barmanapi.CompressionTypeGzip,
				},
			},
		},
	}
}

func endpointCARef(c *controller.Context, logicalName, endpointURL string) (*machineryapi.SecretKeySelector, error) {
	if !strings.HasPrefix(strings.ToLower(endpointURL), "https://") {
		return nil, nil
	}

	name := logicalName + EndpointCASecretSuffix
	secret := &corev1.Secret{}
	if err := c.Get(secret, name); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controller.WaitFor(fmt.Sprintf(
				"endpoint CA Secret %q (key %q) not yet present", name, EndpointCAKey))
		}
		return nil, fmt.Errorf("get endpoint CA Secret %q: %w", name, err)
	}
	if len(secret.Data[EndpointCAKey]) == 0 {
		return nil, &controller.BackupConfigError{
			Reason: "EndpointCAInvalid",
			Message: fmt.Sprintf("Secret %q missing key %q", name, EndpointCAKey),
		}
	}

	return &machineryapi.SecretKeySelector{
		LocalObjectReference: machineryapi.LocalObjectReference{Name: name},
		Key:                  EndpointCAKey,
	}, nil
}

func selectMainStorageName(storages []corev1alpha1.InstanceBackupStorage) string {
	for _, s := range storages {
		if s.Main {
			return s.Name
		}
	}
	for _, s := range storages {
		if s.PITR != nil && s.PITR.Enabled {
			return s.Name
		}
	}
	if len(storages) > 0 {
		return storages[0].Name
	}
	return ""
}
