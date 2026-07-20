package client

func (c *Client) StopScalewayCluster(clusterID string) (*ProviderStopClusterResponse, error) {
	return c.stopProviderCluster("scaleway", clusterID)
}

func (c *Client) StartScalewayCluster(clusterID, scope string) (*ProviderStartClusterResult, error) {
	return c.startProviderCluster("scaleway", clusterID, scope)
}
