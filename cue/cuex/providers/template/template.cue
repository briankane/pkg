package template

#Render: {
	#do:       "render"
	#provider: "template"

	// +usage=The params of this action
	$params: {
		// +usage=The Go text/template to render
		template: string
		// +usage=Anything else the template should be able to reach, under .data
		data?: {...}
	}
	// +usage=The rendered text will be filled in this field after the action is executed
	$returns?: string
	...
}
