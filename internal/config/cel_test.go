package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newTestItem() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetName("test-obj")
	u.SetNamespace("default")
	return u
}

func evalExpr(t *testing.T, expr string, item *unstructured.Unstructured) bool {
	t.Helper()
	ctx := &celItemContext{}
	env, err := newExcludeIfEnv(ctx)
	require.NoError(t, err)
	prog, err := compileExcludeIf(env, expr)
	require.NoError(t, err)
	result, err := evalExcludeIf(prog, ctx, "pods", "", item)
	require.NoError(t, err)
	return result
}

func TestCEL_HasOwnerKind(t *testing.T) {
	trueCtrl := true
	item := newTestItem()
	item.SetOwnerReferences([]metav1.OwnerReference{
		{Kind: "Job", Name: "my-job", Controller: &trueCtrl},
	})

	assert.True(t, evalExpr(t, `hasOwnerKind("Job")`, item))
	assert.False(t, evalExpr(t, `hasOwnerKind("CronJob")`, item))
	assert.False(t, evalExpr(t, `hasOwnerKind("Job")`, newTestItem())) // no owners
}

func TestCEL_HasOwnerName(t *testing.T) {
	item := newTestItem()
	item.SetOwnerReferences([]metav1.OwnerReference{
		{Kind: "CronJob", Name: "nightly-sync"},
	})

	assert.True(t, evalExpr(t, `hasOwnerName("nightly-sync")`, item))
	assert.False(t, evalExpr(t, `hasOwnerName("other")`, item))
	assert.False(t, evalExpr(t, `hasOwnerName("nightly-sync")`, newTestItem()))
}

func TestCEL_IsControlled(t *testing.T) {
	trueCtrl := true
	falseCtrl := false

	controlled := newTestItem()
	controlled.SetOwnerReferences([]metav1.OwnerReference{
		{Kind: "ReplicaSet", Controller: &trueCtrl},
	})

	notControlled := newTestItem()
	notControlled.SetOwnerReferences([]metav1.OwnerReference{
		{Kind: "Something", Controller: &falseCtrl},
	})

	assert.True(t, evalExpr(t, `isControlled()`, controlled))
	assert.False(t, evalExpr(t, `isControlled()`, notControlled))
	assert.False(t, evalExpr(t, `isControlled()`, newTestItem()))
}

func TestCEL_IsGenerated(t *testing.T) {
	generated := newTestItem()
	generated.SetGenerateName("job-runner-")

	assert.True(t, evalExpr(t, `isGenerated()`, generated))
	assert.False(t, evalExpr(t, `isGenerated()`, newTestItem()))
}

func TestCEL_HasLabel(t *testing.T) {
	item := newTestItem()
	item.SetLabels(map[string]string{"app": "nginx", "env": "prod"})

	assert.True(t, evalExpr(t, `hasLabel("app")`, item))
	assert.False(t, evalExpr(t, `hasLabel("missing")`, item))
	assert.False(t, evalExpr(t, `hasLabel("app")`, newTestItem()))
}

func TestCEL_LabelValue(t *testing.T) {
	item := newTestItem()
	item.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "helm"})

	assert.True(t, evalExpr(t, `labelValue("app.kubernetes.io/managed-by") == "helm"`, item))
	assert.True(t, evalExpr(t, `labelValue("missing") == ""`, item))
}

func TestCEL_HasAnnotation(t *testing.T) {
	item := newTestItem()
	item.SetAnnotations(map[string]string{"example.com/retain": "true"})

	assert.True(t, evalExpr(t, `hasAnnotation("example.com/retain")`, item))
	assert.False(t, evalExpr(t, `hasAnnotation("missing")`, item))
	assert.False(t, evalExpr(t, `hasAnnotation("example.com/retain")`, newTestItem()))
}

func TestCEL_AnnotationValue(t *testing.T) {
	item := newTestItem()
	item.SetAnnotations(map[string]string{"note": "keep"})

	assert.True(t, evalExpr(t, `annotationValue("note") == "keep"`, item))
	assert.True(t, evalExpr(t, `annotationValue("missing") == ""`, item))
}

func TestCEL_HasFinalizer(t *testing.T) {
	item := newTestItem()
	item.SetFinalizers([]string{"foregroundDeletion", "my-operator.io/cleanup"})

	assert.True(t, evalExpr(t, `hasFinalizer("foregroundDeletion")`, item))
	assert.True(t, evalExpr(t, `hasFinalizer("my-operator.io/cleanup")`, item))
	assert.False(t, evalExpr(t, `hasFinalizer("other")`, item))
	assert.False(t, evalExpr(t, `hasFinalizer("foregroundDeletion")`, newTestItem()))
}

func TestCEL_IsBeingDeleted(t *testing.T) {
	now := metav1.NewTime(time.Now())

	deleting := newTestItem()
	deleting.SetDeletionTimestamp(&now)

	assert.True(t, evalExpr(t, `isBeingDeleted()`, deleting))
	assert.False(t, evalExpr(t, `isBeingDeleted()`, newTestItem()))
}

func TestCEL_ActivationVariables(t *testing.T) {
	ctx := &celItemContext{}
	env, err := newExcludeIfEnv(ctx)
	require.NoError(t, err)

	item := newTestItem()
	item.SetNamespace("kube-system")
	item.SetName("coredns")

	eval := func(expr, resource, group string) bool {
		prog, err := compileExcludeIf(env, expr)
		require.NoError(t, err)
		result, err := evalExcludeIf(prog, ctx, resource, group, item)
		require.NoError(t, err)
		return result
	}

	assert.True(t, eval(`resource == "pods"`, "pods", ""))
	assert.False(t, eval(`resource == "jobs"`, "pods", ""))
	assert.True(t, eval(`group == "batch"`, "jobs", "batch"))
	assert.True(t, eval(`group == ""`, "pods", ""))
	assert.True(t, eval(`ns == "kube-system"`, "pods", ""))
	assert.True(t, eval(`name == "coredns"`, "pods", ""))
}

func TestCEL_ComposedExpression(t *testing.T) {
	trueCtrl := true
	item := newTestItem()
	item.SetGenerateName("job-")
	item.SetOwnerReferences([]metav1.OwnerReference{
		{Kind: "CronJob", Controller: &trueCtrl},
	})

	assert.True(t, evalExpr(t, `hasOwnerKind("CronJob") && isGenerated()`, item))
	assert.False(t, evalExpr(t, `hasOwnerKind("Job") && isGenerated()`, item))
}

func TestCEL_CompileError(t *testing.T) {
	ctx := &celItemContext{}
	env, err := newExcludeIfEnv(ctx)
	require.NoError(t, err)

	_, err = compileExcludeIf(env, `undeclaredVar == "foo"`)
	assert.Error(t, err)

	_, err = compileExcludeIf(env, `hasOwnerKind(123)`) // wrong arg type
	assert.Error(t, err)

	_, err = compileExcludeIf(env, `hasOwnerKind(`) // syntax error
	assert.Error(t, err)
}
