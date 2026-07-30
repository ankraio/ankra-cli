package client

const scalewayKind = "scaleway"

func (c *Client) StopScalewayCluster(clusterID string) (*ProviderStopClusterResponse, error) {
	return c.stopProviderCluster(scalewayKind, clusterID)
}

func (c *Client) StartScalewayCluster(clusterID, scope string) (*ProviderStartClusterResult, error) {
	return c.startProviderCluster(scalewayKind, clusterID, scope)
}

func (c *Client) ListScalewayClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes(scalewayKind, clusterID)
}

func (c *Client) GetScalewayClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode(scalewayKind, clusterID, nodeID)
}

func (c *Client) RestartScalewayClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode(scalewayKind, clusterID, nodeID)
}
