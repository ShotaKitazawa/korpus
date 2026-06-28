package config

import (
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// celItemContext holds the current object for excludeIf expression evaluation.
// A single instance is shared across all compiled programs within one KorpusConfig.
// It is mutated by evalExcludeIf immediately before prog.Eval — safe because
// runOnce processes objects sequentially in a single goroutine.
type celItemContext struct {
	item *unstructured.Unstructured
}

// newExcludeIfEnv creates a cel.Env whose extension functions read from ctx.
// ctx.item must be set (via evalExcludeIf) before any program evaluation.
func newExcludeIfEnv(ctx *celItemContext) (*cel.Env, error) {
	return cel.NewEnv(
		// Activation variables — available in every excludeIf expression.
		// Note: "namespace" is a reserved identifier in CEL, so the variable is exposed as "ns".
		cel.Variable("resource", cel.StringType), // GVR resource name, e.g. "pods"
		cel.Variable("group", cel.StringType),    // API group, e.g. "batch", "" for core
		cel.Variable("ns", cel.StringType),       // object namespace
		cel.Variable("name", cel.StringType),     // object name

		// ownerReferences group
		cel.Function("hasOwnerKind",
			cel.Overload("korpus_hasOwnerKind_string",
				[]*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(func(kind ref.Val) ref.Val {
					k := string(kind.(types.String))
					for _, o := range ctx.item.GetOwnerReferences() {
						if o.Kind == k {
							return types.True
						}
					}
					return types.False
				}))),

		cel.Function("hasOwnerName",
			cel.Overload("korpus_hasOwnerName_string",
				[]*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(func(name ref.Val) ref.Val {
					n := string(name.(types.String))
					for _, o := range ctx.item.GetOwnerReferences() {
						if o.Name == n {
							return types.True
						}
					}
					return types.False
				}))),

		cel.Function("isControlled",
			cel.Overload("korpus_isControlled",
				[]*cel.Type{}, cel.BoolType,
				cel.FunctionBinding(func(args ...ref.Val) ref.Val {
					for _, o := range ctx.item.GetOwnerReferences() {
						if o.Controller != nil && *o.Controller {
							return types.True
						}
					}
					return types.False
				}))),

		// Generated objects group
		cel.Function("isGenerated",
			cel.Overload("korpus_isGenerated",
				[]*cel.Type{}, cel.BoolType,
				cel.FunctionBinding(func(args ...ref.Val) ref.Val {
					return types.Bool(ctx.item.GetGenerateName() != "")
				}))),

		// Labels group
		cel.Function("hasLabel",
			cel.Overload("korpus_hasLabel_string",
				[]*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(func(key ref.Val) ref.Val {
					k := string(key.(types.String))
					_, ok := ctx.item.GetLabels()[k]
					return types.Bool(ok)
				}))),

		cel.Function("labelValue",
			cel.Overload("korpus_labelValue_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.UnaryBinding(func(key ref.Val) ref.Val {
					k := string(key.(types.String))
					return types.String(ctx.item.GetLabels()[k])
				}))),

		// Annotations group
		cel.Function("hasAnnotation",
			cel.Overload("korpus_hasAnnotation_string",
				[]*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(func(key ref.Val) ref.Val {
					k := string(key.(types.String))
					_, ok := ctx.item.GetAnnotations()[k]
					return types.Bool(ok)
				}))),

		cel.Function("annotationValue",
			cel.Overload("korpus_annotationValue_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.UnaryBinding(func(key ref.Val) ref.Val {
					k := string(key.(types.String))
					return types.String(ctx.item.GetAnnotations()[k])
				}))),

		// Finalizers group
		cel.Function("hasFinalizer",
			cel.Overload("korpus_hasFinalizer_string",
				[]*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(func(key ref.Val) ref.Val {
					k := string(key.(types.String))
					for _, f := range ctx.item.GetFinalizers() {
						if f == k {
							return types.True
						}
					}
					return types.False
				}))),

		// Deletion group
		cel.Function("isBeingDeleted",
			cel.Overload("korpus_isBeingDeleted",
				[]*cel.Type{}, cel.BoolType,
				cel.FunctionBinding(func(args ...ref.Val) ref.Val {
					return types.Bool(ctx.item.GetDeletionTimestamp() != nil)
				}))),
	)
}

// compileExcludeIf compiles a CEL expression using the given env.
// The expression is type-checked at compile time. Returns an error for syntax
// or type errors, enabling fail-fast detection at LoadKorpus time.
func compileExcludeIf(env *cel.Env, expr string) (cel.Program, error) {
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	prog, err := env.Program(ast)
	if err != nil {
		return nil, err
	}
	return prog, nil
}

// evalExcludeIf sets ctx.item then evaluates prog against the given object.
// Returns (false, err) if evaluation fails or the result is not a bool.
func evalExcludeIf(prog cel.Program, ctx *celItemContext, resource, group string, item *unstructured.Unstructured) (bool, error) {
	ctx.item = item
	out, _, err := prog.Eval(map[string]any{
		"resource": resource,
		"group":    group,
		"ns":       item.GetNamespace(),
		"name":     item.GetName(),
	})
	if err != nil {
		return false, err
	}
	b, ok := out.(types.Bool)
	if !ok {
		return false, fmt.Errorf("expression returned %T, want bool", out)
	}
	return bool(b), nil
}
