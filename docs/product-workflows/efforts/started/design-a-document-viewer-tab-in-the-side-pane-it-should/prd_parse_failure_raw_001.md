```markdown
- **schema_version**: idiom.prd.v1
- **prd_id**: design-a-document-viewer-tab-in-the-side-pane-it-should
- **status**: draft
- **task_id**: design-a-document-viewer-tab-in-the-side-pane-it-should
- **created_at**: 2026-05-29T17:02:59Z

- **id**: REQ-001
- **priority**: P1
- **statement**: The document viewer tab in the side-pane must render PRD and ADR markdown files with first-class support, including dedicated formatting and metadata display, regardless of the document's state (e.g., draft, review, approved, deprecated).
- **evidence_refs**: [inference]
- **tdd_obligations**: [TDD-REQ-001]
- **owner_alignment**: Product managers and architects need to quickly review structured PRD and ADR documents without leaving the chat interface, irrespective of the document's lifecycle state.
- **testability**: Verify that when a user clicks on a PRD or ADR file from the chat history, the side-pane displays the document with correct markdown rendering and metadata (e.g., title, status, date) for all possible states of the document.
- **metric**: 100% of PRD and ADR files opened in the viewer render without formatting errors, regardless of their state.
- **open_questions**: []

- **id**: REQ-002
- **priority**: P1
- **statement**: The document viewer tab must support rendering any markdown document that the user clicks from their chat history within the current session.
- **evidence_refs**: [inference]
- **tdd_obligations**: [TDD-REQ-002]
- **owner_alignment**: Users need to view any markdown file referenced in the chat, not just PRD/ADR files, to maintain context and workflow continuity.
- **testability**: Click on a non-PRD/ADR markdown file (e.g., a meeting note or spec) in the chat history and confirm the side-pane renders it correctly.
- **metric**: All markdown files clicked in chat history open in the viewer with no errors.
- **open_questions**: []

- **id**: REQ-003
- **priority**: P1
- **statement**: The system must automatically open the document viewer tab in the side-pane, unprompted, upon the creation of a new ADR or PRD document within the chat interface.
- **evidence_refs**: [operator feedback]
- **tdd_obligations**: [TDD-REQ-003]
- **owner_alignment**: Users should not have to manually click to open the viewer after creating a structured document; the system should proactively display the document for immediate review.
- **testability**: Create a new PRD or ADR document via the chat interface and verify that the document viewer tab automatically opens and displays the newly created document without any additional user action.
- **metric**: 100% of newly created ADR and PRD documents trigger the automatic, unprompted opening of the document viewer tab.
- **open_questions**: []
```