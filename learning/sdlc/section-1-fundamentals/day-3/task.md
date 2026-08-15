# Day 3: Waterfall vs Agile

## 1. Waterfall Sketch

Waterfall follows a linear process. Each phase is mostly completed before moving to the next one.

```text
[Requirements]
      ↓
   [Design]
      ↓
[Implementation]
      ↓
   [Testing]
      ↓
 [Deployment]
      ↓
 [Maintenance]
```

### Strengths

* The process is clear and easy to follow.
* It is easier to plan the budget, timeline, and responsibilities in advance.

### Weaknesses

* Changes can be difficult and expensive after a phase is completed.
* Problems may be discovered late during testing.

---

## 2. Agile Sketch

Agile works in short iterations, usually called sprints. Instead of building everything at once, the team builds a small part of the product, gets feedback, and then improves it in the next iteration.

```text
        ┌─────────────────────────┐
        ↓                         │
   [Plan] → [Build] → [Test] → [Feedback]
        ↑                         │
        └──── [Improve / Next Sprint]
```

For example, a team may first build a basic login system. After users or stakeholders give feedback, the team can improve it or change the priorities for the next sprint.

Agile makes it easier to respond to changing requirements because the product is developed step by step.

---

## 3. Compare for a Project

### Example: Bank Core System

For a bank core system, I would prefer a **Waterfall or Hybrid model**.

Banking systems have strict security, legal, and regulatory requirements. A lot of planning and documentation is needed before implementation. Major changes can also be expensive and risky.

However, Agile practices can still be used for smaller parts of the system, such as internal tools or user interfaces.

### Example: Marketing Landing Page

For a marketing landing page, I would choose **Agile**.

The design and content can change based on user feedback and marketing results. The team can quickly release a version, see how users respond, and improve it in the next iteration.

### Conclusion

The best model depends on the project. A project with strict regulations and stable requirements may benefit from Waterfall, while a project with changing requirements and frequent feedback is usually better suited to Agile.

---

## 4. Hybrid Reality

In real-world software development, many teams do not follow pure Waterfall or pure Agile.

Teams often use a **Hybrid Model**. They may do detailed planning for important areas such as architecture, security, budget, and compliance, while still developing features in short iterations.

For example:

```text
[High-Level Planning]
          ↓
   ┌───────────────┐
   │ Sprint 1      │
   │ Build → Test  │
   └───────┬───────┘
           ↓
   ┌───────────────┐
   │ Sprint 2      │
   │ Build → Test  │
   └───────┬───────┘
           ↓
   ┌───────────────┐
   │ Sprint 3      │
   │ Build → Test  │
   └───────┬───────┘
           ↓
       [Release]
```

This approach gives teams some of the structure of Waterfall while keeping the flexibility of Agile.

## Summary

* **Waterfall:** Sequential and plan-driven.
* **Agile:** Iterative and flexible.
* **Hybrid:** Combines planning with iterative development.
* The right model depends on the project's risk, regulations, requirements, and how often those requirements are expected to change.
