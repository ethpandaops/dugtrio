# Dugtrio Project Instructions

## JavaScript Comment Rules
NEVER add // comments to JavaScript code within HTML template <script> tags as it gets minified and new lines removed which leads to template errors.
Always use /* comment */ format for JavaScript comments in HTML templates to ensure compatibility with minification.
Regular .js files can use // comments normally as they don't get minified.

## General Guidelines
- Follow existing code patterns and conventions in the codebase
- Prefer editing existing files over creating new ones
- Only create files when absolutely necessary for the task