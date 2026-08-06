package products

import (
	"context"
	"errors"
	repo "go-project/internal/adapters/postgresql/sqlc"
)

type Service interface {
	ListProducts(ctx context.Context) ([]repo.Product, error)
	FindProductsByQuery(ctx context.Context, data repo.FindProductsByQueryParams) ([]repo.Product, error)
	FindProductByID(ctx context.Context, id int64) (repo.Product, error)
}
type svc struct {
	// repository
	repo repo.Querier
}

func NewService(repo repo.Querier) Service {
	return &svc{repo: repo}
}

func (s *svc) ListProducts(ctx context.Context) ([]repo.Product, error) {
	return s.repo.ListProducts(ctx)
}

func (s *svc) FindProductsByQuery(ctx context.Context, data repo.FindProductsByQueryParams) ([]repo.Product, error) {
	// Validate data
	if data.PriceInCenters < 0 {
		data.PriceInCenters = 0
	}

	// Query db
	products, err := s.repo.FindProductsByQuery(ctx, data)
	if err != nil {
		return nil, err
	}
	return products, err
}

func (s *svc) FindProductByID(ctx context.Context, id int64) (repo.Product, error) {
	// Validate dât
	if id <= 0 {
		return repo.Product{}, errors.New("ID must be greater than zero!")
	}
	return s.repo.FindProductByID(ctx, id)
}
