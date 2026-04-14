package storage

import (
	"encoding/json"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

type nodeRepo struct {
	db *redka.DB
}

// NewNodeRepo 创建节点仓库
func NewNodeRepo(db *redka.DB) core.NodeRepo {
	return &nodeRepo{db: db}
}

func (r *nodeRepo) Create(node *core.Node) error {
	data, err := json.Marshal(node)
	if err != nil {
		return err
	}
	_, err = r.db.Hash().Set("nodes", node.ID, string(data))
	return err
}

func (r *nodeRepo) GetByID(id string) (*core.Node, error) {
	val, err := r.db.Hash().Get("nodes", id)
	if err != nil {
		return nil, core.ErrNodeNotFound
	}
	var node core.Node
	if err := json.Unmarshal([]byte(val.String()), &node); err != nil {
		return nil, err
	}
	return &node, nil
}

func (r *nodeRepo) GetAll() ([]*core.Node, error) {
	items, err := r.db.Hash().Items("nodes")
	if err != nil {
		return nil, err
	}
	nodes := make([]*core.Node, 0, len(items))
	for _, v := range items {
		var node core.Node
		if err := json.Unmarshal([]byte(v.String()), &node); err != nil {
			continue
		}
		nodes = append(nodes, &node)
	}
	return nodes, nil
}

func (r *nodeRepo) Update(node *core.Node) error {
	data, err := json.Marshal(node)
	if err != nil {
		return err
	}
	_, err = r.db.Hash().Set("nodes", node.ID, string(data))
	return err
}

func (r *nodeRepo) Delete(id string) error {
	_, err := r.db.Hash().Delete("nodes", id)
	return err
}
