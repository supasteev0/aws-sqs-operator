/*
Copyright 2020 Steven Bressey.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SqsSpec defines the desired state of Sqs
type SqsSpec struct {
	// Region is the SQS queue region
	Region string `json:"region"`

	// MaxMessageSize is the limit of how many bytes a message can contain
	// +optional
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=262144
	MaxMessageSize *int `json:"maxMessageSize,omitempty"`
}

// SqsStatus defines the observed state of Sqs
type SqsStatus struct {
	// URL is the url of the SQS queue
	URL string `json:"url"`
	// VisibleMessages is the approximate number of messages available for retrieval from the queue
	// +kubebuilder:validation:Minimum=0
	VisibleMessages int32 `json:"visibleMessages"`
}

// +kubebuilder:object:root=true

// Sqs is the Schema for the sqs API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=sqs-queues,scope=Cluster
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`
// +kubebuilder:printcolumn:name="Messages",type=string,JSONPath=`.status.visibleMessages`
type Sqs struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SqsSpec   `json:"spec,omitempty"`
	Status SqsStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SqsList contains a list of Sqs
type SqsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sqs `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Sqs{}, &SqsList{})
}
