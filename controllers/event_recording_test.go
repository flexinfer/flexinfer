/*
Copyright 2025.

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

package controllers

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// FakeEventRecorder implements record.EventRecorder for testing
type FakeEventRecorder struct {
	Events []FakeEvent
}

type FakeEvent struct {
	Object    runtime.Object
	EventType string
	Reason    string
	Message   string
	Timestamp time.Time
}

func (f *FakeEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	f.Events = append(f.Events, FakeEvent{
		Object:    object,
		EventType: eventtype,
		Reason:    reason,
		Message:   message,
		Timestamp: time.Now(),
	})
}

func (f *FakeEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	f.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

func (f *FakeEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	// For simplicity, ignore annotations in tests
	f.Eventf(object, eventtype, reason, messageFmt, args...)
}

func (f *FakeEventRecorder) GetEventsForObject(obj runtime.Object) []FakeEvent {
	var objectEvents []FakeEvent
	for _, event := range f.Events {
		if event.Object == obj {
			objectEvents = append(objectEvents, event)
		}
	}
	return objectEvents
}

func (f *FakeEventRecorder) GetEventsByReason(reason string) []FakeEvent {
	var reasonEvents []FakeEvent
	for _, event := range f.Events {
		if event.Reason == reason {
			reasonEvents = append(reasonEvents, event)
		}
	}
	return reasonEvents
}

func (f *FakeEventRecorder) GetEventsByType(eventType string) []FakeEvent {
	var typeEvents []FakeEvent
	for _, event := range f.Events {
		if event.EventType == eventType {
			typeEvents = append(typeEvents, event)
		}
	}
	return typeEvents
}

func (f *FakeEventRecorder) Clear() {
	f.Events = []FakeEvent{}
}

func TestFakeEventRecorder(t *testing.T) {
	t.Run("records events correctly", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-model",
				Namespace: "default",
			},
		}

		recorder := &FakeEventRecorder{}

		// Record some events
		recorder.Event(md, corev1.EventTypeNormal, aiv1alpha1.ReasonReconciling, "Starting reconciliation")
		recorder.Event(md, corev1.EventTypeWarning, aiv1alpha1.ReasonValidationFailed, "Validation failed")

		// Verify events were recorded
		assert.Len(t, recorder.Events, 2, "Should record 2 events")

		// Check first event
		assert.Equal(t, md, recorder.Events[0].Object)
		assert.Equal(t, corev1.EventTypeNormal, recorder.Events[0].EventType)
		assert.Equal(t, aiv1alpha1.ReasonReconciling, recorder.Events[0].Reason)
		assert.Equal(t, "Starting reconciliation", recorder.Events[0].Message)

		// Check second event
		assert.Equal(t, md, recorder.Events[1].Object)
		assert.Equal(t, corev1.EventTypeWarning, recorder.Events[1].EventType)
		assert.Equal(t, aiv1alpha1.ReasonValidationFailed, recorder.Events[1].Reason)
		assert.Equal(t, "Validation failed", recorder.Events[1].Message)
	})

	t.Run("Eventf works correctly", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		}

		recorder := &FakeEventRecorder{}
		recorder.Eventf(md, corev1.EventTypeNormal, "TestReason", "Test message with %s and %d", "string", 42)

		require.Len(t, recorder.Events, 1)
		assert.Equal(t, "Test message with string and 42", recorder.Events[0].Message)
	})

	t.Run("GetEventsByReason filters correctly", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		}

		recorder := &FakeEventRecorder{}
		recorder.Event(md, corev1.EventTypeNormal, "Reason1", "Message 1")
		recorder.Event(md, corev1.EventTypeNormal, "Reason2", "Message 2")
		recorder.Event(md, corev1.EventTypeNormal, "Reason1", "Message 3")

		reason1Events := recorder.GetEventsByReason("Reason1")
		assert.Len(t, reason1Events, 2, "Should find 2 events with Reason1")

		reason2Events := recorder.GetEventsByReason("Reason2")
		assert.Len(t, reason2Events, 1, "Should find 1 event with Reason2")
	})

	t.Run("GetEventsByType filters correctly", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		}

		recorder := &FakeEventRecorder{}
		recorder.Event(md, corev1.EventTypeNormal, "Reason1", "Message 1")
		recorder.Event(md, corev1.EventTypeWarning, "Reason2", "Message 2")
		recorder.Event(md, corev1.EventTypeNormal, "Reason3", "Message 3")

		normalEvents := recorder.GetEventsByType(corev1.EventTypeNormal)
		assert.Len(t, normalEvents, 2, "Should find 2 normal events")

		warningEvents := recorder.GetEventsByType(corev1.EventTypeWarning)
		assert.Len(t, warningEvents, 1, "Should find 1 warning event")
	})
}

func TestSpecificEventScenarios(t *testing.T) {
	t.Run("reconcile start event", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-start-event",
				Namespace: "default",
			},
			Spec: aiv1alpha1.ModelDeploymentSpec{
				Backend: "ollama",
				Model:   "llama3:8b",
			},
		}

		fakeRecorder := &FakeEventRecorder{}

		// Simulate just the start of reconciliation
		fakeRecorder.Event(md, corev1.EventTypeNormal, aiv1alpha1.ReasonReconciling, "Starting ModelDeployment reconciliation")

		// Verify event was recorded
		events := fakeRecorder.GetEventsByReason(aiv1alpha1.ReasonReconciling)
		require.Len(t, events, 1, "Should record one reconciling event")

		event := events[0]
		assert.Equal(t, corev1.EventTypeNormal, event.EventType)
		assert.Equal(t, "Starting ModelDeployment reconciliation", event.Message)
		assert.Equal(t, md, event.Object)
	})

	t.Run("service creation events", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service-events",
				Namespace: "default",
			},
		}

		fakeRecorder := &FakeEventRecorder{}

		// Simulate service creation events
		fakeRecorder.Event(md, corev1.EventTypeNormal, "ServiceCreating", "Creating service for ModelDeployment")
		fakeRecorder.Event(md, corev1.EventTypeNormal, "ServiceCreated", "Service created successfully")

		// Verify service creation events
		serviceEvents := fakeRecorder.GetEventsForObject(md)
		require.Len(t, serviceEvents, 2, "Should record two service-related events")

		assert.Equal(t, "ServiceCreating", serviceEvents[0].Reason)
		assert.Equal(t, "Creating service for ModelDeployment", serviceEvents[0].Message)
		assert.Equal(t, corev1.EventTypeNormal, serviceEvents[0].EventType)

		assert.Equal(t, "ServiceCreated", serviceEvents[1].Reason)
		assert.Equal(t, "Service created successfully", serviceEvents[1].Message)
		assert.Equal(t, corev1.EventTypeNormal, serviceEvents[1].EventType)
	})

	t.Run("service creation failure event", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service-fail",
				Namespace: "default",
			},
		}

		fakeRecorder := &FakeEventRecorder{}

		// Simulate service creation failure
		fakeRecorder.Event(md, corev1.EventTypeWarning, "ServiceCreateFailed", "Failed to create service: some error")

		// Verify failure event
		warningEvents := fakeRecorder.GetEventsByType(corev1.EventTypeWarning)
		require.Len(t, warningEvents, 1, "Should record one warning event")

		event := warningEvents[0]
		assert.Equal(t, "ServiceCreateFailed", event.Reason)
		assert.Contains(t, event.Message, "Failed to create service")
	})

	t.Run("reconcile completion event", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-completion",
				Namespace: "default",
			},
		}

		fakeRecorder := &FakeEventRecorder{}

		// Simulate completion event
		fakeRecorder.Event(md, corev1.EventTypeNormal, "ReconcileComplete", "ModelDeployment reconciliation completed successfully")

		// Verify completion event
		completionEvents := fakeRecorder.GetEventsByReason("ReconcileComplete")
		require.Len(t, completionEvents, 1, "Should record one completion event")

		event := completionEvents[0]
		assert.Equal(t, corev1.EventTypeNormal, event.EventType)
		assert.Equal(t, "ModelDeployment reconciliation completed successfully", event.Message)
	})
}

func TestEventRecorderIntegration(t *testing.T) {
	// Integration test demonstrating full event recording flow
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "integration-events",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
			Model:   "llama3:8b",
		},
	}

	fakeRecorder := &FakeEventRecorder{}

	// Simulate complete reconciliation event flow
	events := []struct {
		eventType string
		reason    string
		message   string
	}{
		{corev1.EventTypeNormal, aiv1alpha1.ReasonReconciling, "Starting ModelDeployment reconciliation"},
		{corev1.EventTypeNormal, "ServiceCreating", "Creating service for ModelDeployment"},
		{corev1.EventTypeNormal, "ServiceCreated", "Service created successfully"},
		{corev1.EventTypeNormal, "ReconcileComplete", "ModelDeployment reconciliation completed successfully"},
	}

	// Record all events
	for _, event := range events {
		fakeRecorder.Event(md, event.eventType, event.reason, event.message)
	}

	// Verify complete event flow
	allEvents := fakeRecorder.GetEventsForObject(md)
	require.Len(t, allEvents, len(events), "Should record all expected events")

	// Verify event sequence and content
	for i, expectedEvent := range events {
		actualEvent := allEvents[i]
		assert.Equal(t, expectedEvent.eventType, actualEvent.EventType, "Event %d type should match", i)
		assert.Equal(t, expectedEvent.reason, actualEvent.Reason, "Event %d reason should match", i)
		assert.Equal(t, expectedEvent.message, actualEvent.Message, "Event %d message should match", i)
		assert.False(t, actualEvent.Timestamp.IsZero(), "Event %d should have timestamp", i)
	}

	// Verify event type distribution
	normalEvents := fakeRecorder.GetEventsByType(corev1.EventTypeNormal)
	warningEvents := fakeRecorder.GetEventsByType(corev1.EventTypeWarning)

	assert.Len(t, normalEvents, 4, "Should have 4 normal events")
	assert.Len(t, warningEvents, 0, "Should have 0 warning events")

	// Verify specific event reasons
	reconcilingEvents := fakeRecorder.GetEventsByReason(aiv1alpha1.ReasonReconciling)
	assert.Len(t, reconcilingEvents, 1, "Should have 1 reconciling event")

	completionEvents := fakeRecorder.GetEventsByReason("ReconcileComplete")
	assert.Len(t, completionEvents, 1, "Should have 1 completion event")
}

func TestEventConstants(t *testing.T) {
	// Test that event reason constants are properly defined and used
	t.Run("validate event reason constants", func(t *testing.T) {
		// Test that constants are defined
		assert.NotEmpty(t, aiv1alpha1.ReasonReconciling, "ReasonReconciling should be defined")
		assert.NotEmpty(t, aiv1alpha1.ReasonValidationFailed, "ReasonValidationFailed should be defined")
		assert.NotEmpty(t, aiv1alpha1.ReasonGPUAllocated, "ReasonGPUAllocated should be defined")
		assert.NotEmpty(t, aiv1alpha1.ReasonDeploymentReady, "ReasonDeploymentReady should be defined")
		assert.NotEmpty(t, aiv1alpha1.ReasonServiceReady, "ReasonServiceReady should be defined")

		// Test expected values
		assert.Equal(t, "Reconciling", aiv1alpha1.ReasonReconciling)
		assert.Equal(t, "ValidationFailed", aiv1alpha1.ReasonValidationFailed)
		assert.Equal(t, "GPUAllocated", aiv1alpha1.ReasonGPUAllocated)
		assert.Equal(t, "DeploymentReady", aiv1alpha1.ReasonDeploymentReady)
		assert.Equal(t, "ServiceReady", aiv1alpha1.ReasonServiceReady)
	})

	t.Run("use constants in events", func(t *testing.T) {
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		}

		fakeRecorder := &FakeEventRecorder{}

		// Use constants for event recording
		fakeRecorder.Event(md, corev1.EventTypeNormal, aiv1alpha1.ReasonReconciling, "Test message")
		fakeRecorder.Event(md, corev1.EventTypeWarning, aiv1alpha1.ReasonValidationFailed, "Validation failed")

		// Verify constants were used correctly
		events := fakeRecorder.Events
		require.Len(t, events, 2)

		assert.Equal(t, aiv1alpha1.ReasonReconciling, events[0].Reason)
		assert.Equal(t, aiv1alpha1.ReasonValidationFailed, events[1].Reason)
	})
}
