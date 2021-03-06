/*
Copyright 2017 Heptio Inc.

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

package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kuberrs "k8s.io/apimachinery/pkg/util/errors"

	api "github.com/heptio/ark/pkg/apis/ark/v1"
	"github.com/heptio/ark/pkg/client"
	"github.com/heptio/ark/pkg/cloudprovider"
	"github.com/heptio/ark/pkg/discovery"
	"github.com/heptio/ark/pkg/util/collections"
	kubeutil "github.com/heptio/ark/pkg/util/kube"
	"github.com/heptio/ark/pkg/util/logging"
)

// Backupper performs backups.
type Backupper interface {
	// Backup takes a backup using the specification in the api.Backup and writes backup and log data
	// to the given writers.
	Backup(backup *api.Backup, backupFile, logFile io.Writer, actions []ItemAction) error
}

// kubernetesBackupper implements Backupper.
type kubernetesBackupper struct {
	dynamicFactory        client.DynamicFactory
	discoveryHelper       discovery.Helper
	podCommandExecutor    podCommandExecutor
	groupBackupperFactory groupBackupperFactory
	snapshotService       cloudprovider.SnapshotService
}

type itemKey struct {
	resource  string
	namespace string
	name      string
}

type resolvedAction struct {
	ItemAction

	resourceIncludesExcludes  *collections.IncludesExcludes
	namespaceIncludesExcludes *collections.IncludesExcludes
	selector                  labels.Selector
}

// LogSetter is an interface for a type that allows a FieldLogger
// to be set on it.
type LogSetter interface {
	SetLog(logrus.FieldLogger)
}

func (i *itemKey) String() string {
	return fmt.Sprintf("resource=%s,namespace=%s,name=%s", i.resource, i.namespace, i.name)
}

// NewKubernetesBackupper creates a new kubernetesBackupper.
func NewKubernetesBackupper(
	discoveryHelper discovery.Helper,
	dynamicFactory client.DynamicFactory,
	podCommandExecutor podCommandExecutor,
	snapshotService cloudprovider.SnapshotService,
) (Backupper, error) {
	return &kubernetesBackupper{
		discoveryHelper:       discoveryHelper,
		dynamicFactory:        dynamicFactory,
		podCommandExecutor:    podCommandExecutor,
		groupBackupperFactory: &defaultGroupBackupperFactory{},
		snapshotService:       snapshotService,
	}, nil
}

func resolveActions(actions []ItemAction, helper discovery.Helper) ([]resolvedAction, error) {
	var resolved []resolvedAction

	for _, action := range actions {
		resourceSelector, err := action.AppliesTo()
		if err != nil {
			return nil, err
		}

		resources := getResourceIncludesExcludes(helper, resourceSelector.IncludedResources, resourceSelector.ExcludedResources)
		namespaces := collections.NewIncludesExcludes().Includes(resourceSelector.IncludedNamespaces...).Excludes(resourceSelector.ExcludedNamespaces...)

		selector := labels.Everything()
		if resourceSelector.LabelSelector != "" {
			if selector, err = labels.Parse(resourceSelector.LabelSelector); err != nil {
				return nil, err
			}
		}

		res := resolvedAction{
			ItemAction:                action,
			resourceIncludesExcludes:  resources,
			namespaceIncludesExcludes: namespaces,
			selector:                  selector,
		}

		resolved = append(resolved, res)
	}

	return resolved, nil
}

// getResourceIncludesExcludes takes the lists of resources to include and exclude, uses the
// discovery helper to resolve them to fully-qualified group-resource names, and returns an
// IncludesExcludes list.
func getResourceIncludesExcludes(helper discovery.Helper, includes, excludes []string) *collections.IncludesExcludes {
	resources := collections.GenerateIncludesExcludes(
		includes,
		excludes,
		func(item string) string {
			gvr, _, err := helper.ResourceFor(schema.ParseGroupResource(item).WithVersion(""))
			if err != nil {
				return ""
			}

			gr := gvr.GroupResource()
			return gr.String()
		},
	)

	return resources
}

// getNamespaceIncludesExcludes returns an IncludesExcludes list containing which namespaces to
// include and exclude from the backup.
func getNamespaceIncludesExcludes(backup *api.Backup) *collections.IncludesExcludes {
	return collections.NewIncludesExcludes().Includes(backup.Spec.IncludedNamespaces...).Excludes(backup.Spec.ExcludedNamespaces...)
}

func getResourceHooks(hookSpecs []api.BackupResourceHookSpec, discoveryHelper discovery.Helper) ([]resourceHook, error) {
	resourceHooks := make([]resourceHook, 0, len(hookSpecs))

	for _, r := range hookSpecs {
		h := resourceHook{
			name:       r.Name,
			namespaces: collections.NewIncludesExcludes().Includes(r.IncludedNamespaces...).Excludes(r.ExcludedNamespaces...),
			resources:  getResourceIncludesExcludes(discoveryHelper, r.IncludedResources, r.ExcludedResources),
			hooks:      r.Hooks,
		}

		if r.LabelSelector != nil {
			labelSelector, err := metav1.LabelSelectorAsSelector(r.LabelSelector)
			if err != nil {
				return []resourceHook{}, errors.WithStack(err)
			}
			h.labelSelector = labelSelector
		}

		resourceHooks = append(resourceHooks, h)
	}

	return resourceHooks, nil
}

// Backup backs up the items specified in the Backup, placing them in a gzip-compressed tar file
// written to backupFile. The finalized api.Backup is written to metadata.
func (kb *kubernetesBackupper) Backup(backup *api.Backup, backupFile, logFile io.Writer, actions []ItemAction) error {
	gzippedData := gzip.NewWriter(backupFile)
	defer gzippedData.Close()

	tw := tar.NewWriter(gzippedData)
	defer tw.Close()

	gzippedLog := gzip.NewWriter(logFile)
	defer gzippedLog.Close()

	logger := logrus.New()
	logger.Out = gzippedLog
	logger.Hooks.Add(&logging.ErrorLocationHook{})
	logger.Hooks.Add(&logging.LogLocationHook{})
	log := logger.WithField("backup", kubeutil.NamespaceAndName(backup))
	log.Info("Starting backup")

	namespaceIncludesExcludes := getNamespaceIncludesExcludes(backup)
	log.Infof("Including namespaces: %s", namespaceIncludesExcludes.IncludesString())
	log.Infof("Excluding namespaces: %s", namespaceIncludesExcludes.ExcludesString())

	resourceIncludesExcludes := getResourceIncludesExcludes(kb.discoveryHelper, backup.Spec.IncludedResources, backup.Spec.ExcludedResources)
	log.Infof("Including resources: %s", resourceIncludesExcludes.IncludesString())
	log.Infof("Excluding resources: %s", resourceIncludesExcludes.ExcludesString())

	resourceHooks, err := getResourceHooks(backup.Spec.Hooks.Resources, kb.discoveryHelper)
	if err != nil {
		return err
	}

	var labelSelector string
	if backup.Spec.LabelSelector != nil {
		labelSelector = metav1.FormatLabelSelector(backup.Spec.LabelSelector)
	}

	backedUpItems := make(map[itemKey]struct{})
	var errs []error

	cohabitatingResources := map[string]*cohabitatingResource{
		"deployments":     newCohabitatingResource("deployments", "extensions", "apps"),
		"networkpolicies": newCohabitatingResource("networkpolicies", "extensions", "networking.k8s.io"),
	}

	resolvedActions, err := resolveActions(actions, kb.discoveryHelper)
	if err != nil {
		return err
	}

	gb := kb.groupBackupperFactory.newGroupBackupper(
		log,
		backup,
		namespaceIncludesExcludes,
		resourceIncludesExcludes,
		labelSelector,
		kb.dynamicFactory,
		kb.discoveryHelper,
		backedUpItems,
		cohabitatingResources,
		resolvedActions,
		kb.podCommandExecutor,
		tw,
		resourceHooks,
		kb.snapshotService,
	)

	for _, group := range kb.discoveryHelper.Resources() {
		if err := gb.backupGroup(group); err != nil {
			errs = append(errs, err)
		}
	}

	err = kuberrs.NewAggregate(errs)
	if err == nil {
		log.Infof("Backup completed successfully")
	} else {
		log.Infof("Backup completed with errors: %v", err)
	}

	return err
}

type tarWriter interface {
	io.Closer
	Write([]byte) (int, error)
	WriteHeader(*tar.Header) error
}
