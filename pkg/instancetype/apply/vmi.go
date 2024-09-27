/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2024 Red Hat, Inc.
 *
 */
package apply

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfield "k8s.io/apimachinery/pkg/util/validation/field"

	virtv1 "kubevirt.io/api/core/v1"
	v1beta1 "kubevirt.io/api/instancetype/v1beta1"

	preferenceApply "kubevirt.io/kubevirt/pkg/instancetype/preference/apply"
)

type VMIApplier struct {
	preferenceApplier *preferenceApply.VMIApplier
}

func NewVMIApplier() *VMIApplier {
	return &VMIApplier{
		preferenceApplier: &preferenceApply.VMIApplier{},
	}
}

func (a *VMIApplier) ApplyToVMI(
	field *k8sfield.Path,
	instancetypeSpec *v1beta1.VirtualMachineInstancetypeSpec,
	preferenceSpec *v1beta1.VirtualMachinePreferenceSpec,
	vmiSpec *virtv1.VirtualMachineInstanceSpec,
	vmiMetadata *metav1.ObjectMeta,
) (conflicts Conflicts) {
	if instancetypeSpec == nil && preferenceSpec == nil {
		return
	}

	if instancetypeSpec != nil {
		conflicts = append(conflicts, applyNodeSelector(field, instancetypeSpec, vmiSpec)...)
		conflicts = append(conflicts, applySchedulerName(field, instancetypeSpec, vmiSpec)...)
		conflicts = append(conflicts, applyCPU(field, instancetypeSpec, preferenceSpec, vmiSpec)...)
		conflicts = append(conflicts, applyMemory(field, instancetypeSpec, vmiSpec)...)
		conflicts = append(conflicts, applyIOThreadPolicy(field, instancetypeSpec, vmiSpec)...)
		conflicts = append(conflicts, applyLaunchSecurity(field, instancetypeSpec, vmiSpec)...)
		conflicts = append(conflicts, applyGPUs(field, instancetypeSpec, vmiSpec)...)
		conflicts = append(conflicts, applyHostDevices(field, instancetypeSpec, vmiSpec)...)
		conflicts = append(conflicts, applyInstanceTypeAnnotations(instancetypeSpec.Annotations, vmiMetadata)...)
	}

	if len(conflicts) > 0 {
		return
	}

	a.preferenceApplier.Apply(preferenceSpec, vmiSpec, vmiMetadata)

	return
}
