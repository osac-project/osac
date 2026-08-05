package v1alpha1

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func (co *ClusterOrder) RemoveStatusCondition(conditionType string) bool {
	return apimeta.RemoveStatusCondition(&co.Status.Conditions, conditionType)
}

func (co ClusterOrder) IsStatusConditionFalse(conditionType string) bool {
	return apimeta.IsStatusConditionFalse(co.Status.Conditions, conditionType)
}

func (co ClusterOrder) IsStatusConditionTrue(conditionType string) bool {
	return apimeta.IsStatusConditionTrue(co.Status.Conditions, conditionType)
}

func (co ClusterOrder) IsStatusConditionPresentAndEqual(conditionType string, status metav1.ConditionStatus) bool {
	return apimeta.IsStatusConditionPresentAndEqual(co.Status.Conditions, conditionType, status)
}
