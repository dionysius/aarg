package compose

import (
	"fmt"
	"os"
	"path/filepath"
)

// Generate404HTML creates a minimal 404.html file at the root of the staging directory.
// This prevents Cloudflare Pages from treating the site as a Single Page Application,
// which would interfere with _redirects functionality.
func Generate404HTML(stagingPath string) error {
	notFoundPath := filepath.Join(stagingPath, "404.html")

	f, err := os.Create(notFoundPath)
	if err != nil {
		return fmt.Errorf("failed to create 404.html: %w", err)
	}
	defer func() { _ = f.Close() }()

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>404 Not Found</title>
    <style>
        :root {
            --bg-primary: #ffffff;
            --text-primary: #333;
            --text-secondary: #7f8c8d;
            --text-error: #e74c3c;
            --accent: #3498db;
        }
        @media (prefers-color-scheme: dark) {
            :root {
                --bg-primary: #1a1a1a;
                --text-primary: #e0e0e0;
                --text-secondary: #a0a0a0;
                --text-error: #ff6b6b;
                --accent: #5dade2;
            }
        }
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
            max-width: 600px;
            margin: 100px auto;
            padding: 20px;
            text-align: center;
            color: var(--text-primary);
            background: var(--bg-primary);
        }
        h1 {
            color: var(--text-error);
            font-size: 72px;
            margin: 0;
        }
        h2 {
            color: var(--text-primary);
            margin: 10px 0;
        }
        p {
            color: var(--text-secondary);
            line-height: 1.6;
        }
        a {
            color: var(--accent);
            text-decoration: none;
        }
        a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <h1>404</h1>
    <h2>Page Not Found</h2>
    <p>The requested resource could not be found.</p>
    <p><a href="/">Return to Index</a></p>
</body>
</html>
`

	if _, err := f.WriteString(html); err != nil {
		return fmt.Errorf("failed to write 404.html: %w", err)
	}

	return nil
}
