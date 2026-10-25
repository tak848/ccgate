# Contributing

Thank you for considering contributing to ccgate!

## How to contribute

1. Fork the repository
2. Create a feature branch (`git switch -c feature/my-change`)
3. Make your changes
4. Run tests (`mise run test`)
5. Run linter (`mise run vet`)
6. Commit your changes
7. Push to the branch and open a Pull Request

## Development setup

```bash
# Install Go via mise
mise install

# Build
mise run build

# Test
mise run test
```

`mise.lock` pins the tool versions in `mise.toml` with per-platform checksums, and CI installs with `mise install --locked`. If you change a version in `mise.toml`, run `mise lock` and commit the updated `mise.lock`; without `--platform` it refreshes the platforms already in the file.

## Reporting issues

Please open an issue on GitHub with a clear description of the problem and steps to reproduce it.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
