package flow

// DefaultRegistryCapacity is the capacity component registry is initialized with.
const defaultRegistryCapacity = 64

// ComponentConstructor is a function that can be registered in the ComponentRegistry
// so that it is used when creating new processes of a specific component using
// Factory function at run-time.
type ComponentConstructor func() interface{}

// ComponentRegistry is used to register components and spawn processes given just
// a string component name.
var componentRegistry = make(map[string]ComponentConstructor, defaultRegistryCapacity)

// Register registers a component so that it can be instantiated at run-time using component Factory.
// It returns true on success or false if component name is already taken.
func Register(componentName string, constructor ComponentConstructor) bool {
	if _, exists := componentRegistry[componentName]; exists {
		// Component already registered
		return false
	}
	componentRegistry[componentName] = constructor
	return true
}

// Unregister removes a component with a given name from the component registry and returns true
// or returns false if no such component is registered.
func Unregister(componentName string) bool {
	if _, exists := componentRegistry[componentName]; exists {
		delete(componentRegistry, componentName)
		return true
	} else {
		return false
	}
}

// Factory creates a new instance of a component registered under a specific name.
func factory(componentName string) interface{} {
	if constructor, exists := componentRegistry[componentName]; exists {
		return constructor()
	} else {
		panic("Uknown component name: " + componentName)
	}
}
