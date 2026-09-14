package v1alpha1

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SetStatusCondition adds or updates a condition in the ClusterOrder status.
func (co *ClusterOrder) SetStatusCondition(conditionType string, status metav1.ConditionStatus, message string, reason string) bool {
	condition := metav1.Condition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: message,
	}
	if co.Status.Conditions == nil {
		co.Status.Conditions = []metav1.Condition{}
	}
	return apimeta.SetStatusCondition(&co.Status.Conditions, condition)
}

// RemoveStatusCondition removes the condition with the given type from the ClusterOrder status.
func (co *ClusterOrder) RemoveStatusCondition(conditionType string) bool {
	return apimeta.RemoveStatusCondition(&co.Status.Conditions, conditionType)
}

// IsStatusConditionFalse returns true if the condition with the given type is present and set to False.
func (co ClusterOrder) IsStatusConditionFalse(conditionType string) bool {
	return apimeta.IsStatusConditionFalse(co.Status.Conditions, conditionType)
}

// IsStatusConditionTrue returns true if the condition with the given type is present and set to True.
func (co ClusterOrder) IsStatusConditionTrue(conditionType string) bool {
	return apimeta.IsStatusConditionTrue(co.Status.Conditions, conditionType)
}

// IsStatusConditionPresentAndEqual returns true if the condition with the given type is present and equal to the given status.
func (co ClusterOrder) IsStatusConditionPresentAndEqual(conditionType string, status metav1.ConditionStatus) bool {
	return apimeta.IsStatusConditionPresentAndEqual(co.Status.Conditions, conditionType, status)
}
