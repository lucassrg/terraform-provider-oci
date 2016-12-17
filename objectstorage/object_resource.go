package objectstorage

import (
	"github.com/MustWin/terraform-Oracle-BareMetal-Provider/client"
	"github.com/MustWin/terraform-Oracle-BareMetal-Provider/crud"
	"github.com/hashicorp/terraform/helper/schema"
)

func ObjectResource() *schema.Resource {
	return &schema.Resource{
		Create: createObject,
		Read:   readObject,
		Update: updateObject,
		Delete: deleteObject,
		Schema: objectSchema,
	}
}

func createObject(d *schema.ResourceData, m interface{}) (e error) {
	sync := &ObjectResourceCrud{D: d, Client: m.(client.BareMetalClient)}
	return crud.CreateResource(d, sync)
}

func readObject(d *schema.ResourceData, m interface{}) (e error) {
	sync := &ObjectResourceCrud{D: d, Client: m.(client.BareMetalClient)}
	return crud.ReadResource(sync)
}

func updateObject(d *schema.ResourceData, m interface{}) (e error) {
	sync := &ObjectResourceCrud{D: d, Client: m.(client.BareMetalClient)}
	return crud.UpdateResource(d, sync)
}

func deleteObject(d *schema.ResourceData, m interface{}) (e error) {
	sync := &ObjectResourceCrud{D: d, Client: m.(client.BareMetalClient)}
	return crud.DeleteResource(sync)
}