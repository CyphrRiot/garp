# find-words

A high-performance document search tool for legal discovery and forensic analysis. Built in Go for speed and reliability, `find-words` finds files containing ALL specified search terms and displays clean, readable excerpts with highlighted matches.

## Features

- **Multi-word AND logic**: Find files containing ALL search terms (not just any)
- **Clean content extraction**: Strips HTML, CSS, email headers, and other markup
- **File type filtering**: Documents by default, programming files with `--code`
- **Exclusion support**: Filter out files containing unwanted terms with `--not`
- **Large file handling**: Automatically limits search scope for files >10MB
- **Interactive results**: Page through results with highlighted search terms
- **Fast performance**: Efficient Go implementation with ripgrep integration

## Requirements

- **ripgrep** (`rg`) - Install with your package manager:
  ```bash
  # Arch Linux
  sudo pacman -S ripgrep
  
  # Ubuntu/Debian
  sudo apt install ripgrep
  
  # macOS
  brew install ripgrep
  ```

## Installation

### Build from Source

```bash
# Clone the repository
git clone <repository-url>
cd find-words

# Build the binary
make

# Install to ~/.local/bin (ensure it's in your PATH)
make install
```

### Pre-built Binaries

Download pre-built binaries from the [Releases](../../releases) page.

## Usage

### Basic Search
Find files containing ALL specified words:
```bash
find-words contract payment agreement
```

### With Exclusions
Exclude files containing specific terms:
```bash
find-words chris incentive --not test demo fake
```

### Include Programming Files
Search both documents and code files:
```bash
find-words --code function database --not example
```

### Legal Discovery Example
```bash
find-words ethereum blockchain --not scam --not demo --not test
```

## File Types

### Document Files (Default)
- Text: `.txt`, `.md`, `.log`
- Web: `.html`, `.xml`, `.csv`, `.yaml`, `.json`
- Email: `.eml`, `.mbox`, `.msg`
- Office: `.pdf`, `.doc`, `.docx`, `.xls`, `.xlsx`, `.ppt`, `.pptx`
- OpenOffice: `.odt`, `.ods`, `.odp`
- Other: `.rtf`, `.cfg`, `.conf`, `.ini`, `.sh`, `.bat`

### Programming Files (`--code` flag)
- Languages: `.js`, `.ts`, `.py`, `.php`, `.java`, `.cpp`, `.c`, `.go`, `.rs`, `.rb`
- Data: `.sql`, `.json`
- And more...

## Output Format

```
🔍 Multi-Word Search (ripgrep)
Searching for: "contract" "payment" "agreement"
Excluding files with: "test" "demo"
Document files to search: 1,247
Estimated time: 30 seconds - 2 minutes

📄 File 1/3: /path/to/contract.pdf
    🔗 file:///path/to/contract.pdf
    📋 Content matches:
    The payment schedule outlined in this contract specifies that all 
    agreement terms must be met before the final payment is released.

[Press ENTER for next file, 'q' + ENTER to quit]
```

## Performance

- **Fast**: Handles 1000+ document files in under 30 seconds
- **Memory efficient**: Stream processing with automatic size limits
- **Large file support**: Automatically limits search scope for files >10MB
- **Progress tracking**: Real-time progress updates for long searches

## Build Commands

```bash
make           # Build the binary to bin/find-words
make clean     # Remove build artifacts
make test      # Run tests
make fmt       # Format Go code
make install   # Install to ~/.local/bin
make uninstall # Remove from ~/.local/bin
make help      # Show all available commands
```

## Development

### Project Structure
```
find-words/
├── main.go              # CLI interface and argument parsing
├── search/              # Core search logic
│   ├── engine.go        # Main search coordination
│   ├── filter.go        # File discovery and filtering
│   └── cleaner.go       # Content cleaning and highlighting
├── config/              # Configuration
│   └── types.go         # File type definitions
├── bin/                 # Built binaries
├── go.mod               # Go module definition
├── Makefile             # Build configuration
└── README.md            # This file
```

### Development Build
```bash
make dev    # Build with race detection
```

## Use Cases

### Legal Discovery
Find contracts, agreements, and communications:
```bash
find-words contract settlement --not template --not example
find-words "john doe" payment --not test
```

### Forensic Analysis
Search for specific terms while excluding noise:
```bash
find-words bitcoin cryptocurrency --not news --not article
find-words password credential --not documentation
```

### Code Analysis
Search through codebases:
```bash
find-words --code database connection --not test --not mock
find-words --code api key secret --not example
```

## License

[License information here]

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests: `make test`
5. Format code: `make fmt`
6. Submit a pull request

## Support

For issues and feature requests, please open an issue on GitHub.
