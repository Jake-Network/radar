package main

import "fmt"

type ExportJob struct {
	ID             string
	OrganizationID string
	DatasetID      string
}

func ObjectKey(job ExportJob) string {
	return fmt.Sprintf("organizations/%s/exports/%s.csv", job.OrganizationID, job.ID)
}

func main() {
	fmt.Println(ObjectKey(ExportJob{ID: "example", OrganizationID: "org-1"}))
}
