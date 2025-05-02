# Generated Code
The Config Sync repository
deals with two flavors of generated code:

1.  ClientSets for sending requests to the API Server
    -   `clientgen`
2.  DeepCopy methods to make types compatible with `client.Client`
    -   `controller-gen`

Neither of these tools is well documented, so this document collects what we've
learned about how to use them for our use case.

For an example properly-formatted package, see the
[configsync package](https://github.com/GoogleContainerTools/kpt-config-sync/blob/main/pkg/api/configsync/v1beta1/doc.go).

## 1. `clientgen`

The `make clientgen` target handles this.

Note: Because of how this make target is written, you **must** always run `make
generate` after.

To generate a ClientSet for all Kubernetes types in a package, add the following
line to the package docstring:

```go
// +groupName=configmanagement.gke.io
```

Replace `groupName` with the API Group of the types in the package. This must
exactly match the API Group defined in any relevant CRDs.

Before every type which needs a ClientSet, add the following line **before** the
type docstring:

```go
// +genclient

// FooType is a Foo type.
type FooType struct {
```

Note the empty line between the genclient directive and the type docstring.

If the type is cluster-scoped, you must specify this immediately under the
`genclient` directive.

```go
// +genclient
// +genclient:nonNamespaced
```

## 2. `controller-gen`

The `make generate` target handles this.

Add the following line to the package docstring:

```go
// +kubebuilder:object:generate=true
```

This generates all flavors of DeepCopy methods to all types, which is mandatory
to use any of the types with `client.Client` methods.

**Before** the type docstring of any type you need to use with a
`client.Client`, add the following comment:

```go
// +kubebuilder:object:root=true

// FooType is a Foo type.
type FooType struct {
```

Note the empty line between the kubebuilder directive and the type docstring.

You must also do this for any `*List` types, so

```go
// +kubebuilder:object:root=true

// FooTypeList is List of Foo type.
type FooTypeList struct {
```

Note: the `kubebuilder:printcolumn` directives are for
[CRD generation](https://book.kubebuilder.io/reference/generating-crd.html#additional-printer-columns).