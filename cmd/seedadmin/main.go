package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	_ = godotenv.Load(".env")
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Database ping failed: %v", err)
	}

	// List existing users
	rows, err := pool.Query(ctx, "SELECT id, email, display_name, phone_number, role, status FROM users LIMIT 10")
	if err == nil {
		fmt.Println("--- Existing users in DB ---")
		for rows.Next() {
			var id, email, dname, phone, role, status string
			if err := rows.Scan(&id, &email, &dname, &phone, &role, &status); err == nil {
				fmt.Printf("[%s] email=%s, role=%s, status=%s, phone=%s, name=%s\n", id, email, role, status, phone, dname)
			}
		}
		rows.Close()
	}

	adminEmail := os.Getenv("ADMIN_SEED_EMAIL")
	if adminEmail == "" {
		adminEmail = "admin@paysplit.app"
	}
	adminPassword := os.Getenv("ADMIN_SEED_PASSWORD")
	if adminPassword == "" {
		adminPassword = "Admin@123456"
	}
	adminDisplayName := os.Getenv("ADMIN_SEED_NAME")
	if adminDisplayName == "" {
		adminDisplayName = "PaySplit Admin"
	}
	adminPhone := os.Getenv("ADMIN_SEED_PHONE")
	if adminPhone == "" {
		adminPhone = "+84999999999"
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	// Check if admin already exists by email
	var existingID string
	err = pool.QueryRow(ctx, "SELECT id FROM users WHERE email = $1", adminEmail).Scan(&existingID)
	if err == nil {
		// Update
		_, err = pool.Exec(ctx, `
			UPDATE users 
			SET password_hash = $1, display_name = $2, role = 'admin', status = 'active', email_verified_at = now(), updated_at = now()
			WHERE id = $3
		`, string(hash), adminDisplayName, existingID)
		if err != nil {
			log.Fatalf("Failed to update admin: %v", err)
		}
		fmt.Printf("Admin updated successfully! ID: %s, Email: %s\n", existingID, adminEmail)
	} else {
		// Insert
		var id string
		err = pool.QueryRow(ctx, `
			INSERT INTO users (
				email, password_hash, display_name, phone_number, role, status, email_verified_at, created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'admin', 'active', now(), now(), now())
			RETURNING id
		`, adminEmail, string(hash), adminDisplayName, adminPhone).Scan(&id)
		if err != nil {
			log.Fatalf("Failed to insert admin: %v", err)
		}
		fmt.Printf("Admin created successfully! ID: %s, Email: %s\n", id, adminEmail)
	}

	fmt.Println("==================================================")
	fmt.Println("   Admin Credentials:")
	fmt.Printf("   Email:    %s\n", adminEmail)
	fmt.Printf("   Password: %s\n", adminPassword)
	fmt.Println("==================================================")
}
