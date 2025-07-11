package client

import (
	"fmt"
)

type OperationResponseListItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type OperationListResponse struct {
	Result     []OperationResponseListItem `json:"result"`
	Pagination Pagination                  `json:"pagination"`
}

func ListClusterOperations(token, baseURL, clusterID string) ([]OperationResponseListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/operations?type_list=write", baseURL, clusterID)
	var operations OperationListResponse
	if err := getJSON(url, token, &operations.Result); err != nil {
		return nil, err
	}
	return operations.Result, nil
}
