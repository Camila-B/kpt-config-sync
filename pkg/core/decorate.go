// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package core

// Annotated is the interface defined by types with annotations. Note that
// some non-objects (such as PodTemplates) define annotations but are not objects.
type Annotated interface {
	GetAnnotations() map[string]string
	SetAnnotations(annotations map[string]string)
}

// SetAnnotation sets the annotation on the passed annotated object to value.
func SetAnnotation(obj Annotated, annotation, value string) {
	as := obj.GetAnnotations()
	if as == nil {
		as = make(map[string]string)
	}
	as[annotation] = value
	obj.SetAnnotations(as)
}

// GetAnnotation gets the annotation value on the passed annotated object for a given key.
func GetAnnotation(obj Annotated, annotation string) string {
	as := obj.GetAnnotations()
	if as == nil {
		return ""
	}
	value, found := as[annotation]
	if found {
		return value
	}
	return ""
}

// GetLabel gets the label value on the passed object for a given key.
func GetLabel(obj Labeled, label string) string {
	as := obj.GetLabels()
	if as == nil {
		return ""
	}
	value, found := as[label]
	if found {
		return value
	}
	return ""
}

// RemoveAnnotations removes the passed set of annotations from obj.
func RemoveAnnotations(obj Annotated, annotations ...string) {
	as := obj.GetAnnotations()
	for _, a := range annotations {
		delete(as, a)
	}
	obj.SetAnnotations(as)
}

// AddAnnotations adds the specified annotations to the object.
func AddAnnotations(obj Annotated, annotations map[string]string) {
	existing := obj.GetAnnotations()
	if existing == nil {
		existing = make(map[string]string, len(annotations))
	}
	for key, value := range annotations {
		existing[key] = value
	}
	obj.SetAnnotations(existing)
}

// Labeled is the interface defined by types with labeled. Note that
// some non-objects (such as PodTemplates) define labels but are not objects.
type Labeled interface {
	GetLabels() map[string]string
	SetLabels(annotations map[string]string)
}

// SetLabel sets label on obj to value.
func SetLabel(obj Labeled, label, value string) {
	ls := obj.GetLabels()
	if ls == nil {
		ls = make(map[string]string)
	}
	ls[label] = value
	obj.SetLabels(ls)
}

// AddLabels adds the specified labels to the object.
func AddLabels(obj Labeled, labels map[string]string) {
	existing := obj.GetLabels()
	if existing == nil {
		existing = make(map[string]string, len(labels))
	}
	for key, value := range labels {
		existing[key] = value
	}
	obj.SetLabels(existing)
}

// copyMap returns a copy of the passed map. Otherwise the Labels or Annotations maps will have two
// owners.
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string)
	for k, v := range m {
		result[k] = v
	}
	return result
}
