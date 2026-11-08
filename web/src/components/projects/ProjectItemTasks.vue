<template>
  <div class="item-tasks">
    <!-- Task list -->
    <div
      v-for="task in tasks"
      :key="task.id"
      class="item-tasks__task"
      :class="{ 'item-tasks__task--completed': task.completed }">
      <button
        class="item-tasks__check"
        :title="task.completed ? 'Mark incomplete' : 'Mark complete'"
        @click="toggleTask(task)">
        <i :class="task.completed ? 'fa-solid fa-square-check' : 'fa-regular fa-square'"></i>
      </button>
      <span class="item-tasks__task-title">{{ task.title }}</span>
      <button
        class="item-tasks__delete"
        title="Delete task"
        @click="deleteTask(task.id)">
        <i class="fa-solid fa-xmark"></i>
      </button>
    </div>

    <!-- Add task: icon button that expands to input inline -->
    <form
      v-if="addingTask"
      class="item-tasks__add"
      @submit.prevent="addTask">
      <input
        ref="newTaskInput"
        v-model="newTaskTitle"
        type="text"
        class="textbox item-tasks__input"
        placeholder="Task title..."
        @keydown.esc="cancelAdd"
        @blur="cancelAdd" />
    </form>
    <button
      v-else
      class="item-tasks__add-icon"
      title="Add task"
      @click="startAdd">
      <i class="fa-solid fa-plus"></i>
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted } from 'vue'
import { useProjectsStore } from '@/stores/projects'
import { useNotifications } from '@/composables/useNotifications'
import { ApiError } from '@/api/errors'

const props = defineProps<{
  itemId: string
}>()

const store = useProjectsStore()
const { show: notify } = useNotifications()

const newTaskTitle = ref('')
const newTaskInput = ref<HTMLInputElement | null>(null)
const addingTask = ref(false)

const tasks = computed(() => store.itemTasks[props.itemId] ?? [])

onMounted(() => {
  store.fetchItemTasks(props.itemId)
})

async function startAdd() {
  addingTask.value = true
  await nextTick()
  newTaskInput.value?.focus()
}

function cancelAdd() {
  // Small delay so submit fires before blur hides the input
  setTimeout(() => {
    addingTask.value = false
    newTaskTitle.value = ''
  }, 150)
}

async function addTask() {
  const title = newTaskTitle.value.trim()
  addingTask.value = false
  newTaskTitle.value = ''
  if (!title) return
  try {
    await store.createItemTask(props.itemId, { title })
    notify(`Task added`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to add task: ${detail}`, 'error')
  }
}

async function toggleTask(task: { id: string; completed: boolean }) {
  try {
    await store.updateItemTask(props.itemId, task.id, { completed: !task.completed })
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to update task: ${detail}`, 'error')
  }
}

async function deleteTask(taskId: string) {
  try {
    await store.removeItemTask(props.itemId, taskId)
    notify(`Task deleted`, 'success')
  } catch (e) {
    const detail = e instanceof ApiError ? e.userMessage : String(e)
    notify(`Failed to delete task: ${detail}`, 'error')
  }
}
</script>

<style scoped lang="scss">
.item-tasks {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  padding: 0.5rem 0 0;
  border-top: 1px solid var(--clr-gray-trans-875);

  &__empty {
    color: var(--clr-gray-300);
    font-style: italic;
    font-size: 0.8rem;
  }

  &__task {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.2rem 0;

    &--completed {
      opacity: 0.55;

      .item-tasks__task-title {
        text-decoration: line-through;
      }
    }
  }

  &__check {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0;
    font-size: 0.85rem;
    color: var(--clr-secondary);
    flex-shrink: 0;
    transition: color var(--transition-fast) ease;

    &:hover {
      color: var(--clr-success);
    }
  }

  &__task-title {
    flex: 1;
    font-size: 0.85rem;
    min-width: 0;
    color: var(--clr-gray-100);
  }

  &__delete {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0 0.15rem;
    font-size: 0.7rem;
    color: var(--clr-gray-300);
    opacity: 0;
    transition:
      opacity 0.15s ease,
      color 0.1s ease;

    .item-tasks__task:hover & {
      opacity: 1;
    }

    &:hover {
      color: var(--clr-error);
    }
  }

  &__add {
    margin-top: 0.25rem;
  }

  &__input {
    width: 100%;
    font-size: 0.8rem;
    padding: 0.2rem 0.5rem;
  }

  &__add-icon {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.1rem 0.25rem;
    margin-top: 0.25rem;
    font-size: 0.75rem;
    color: var(--clr-subtle);
    transition: color var(--transition-fast) ease;

    &:hover {
      color: var(--clr-secondary);
    }
  }
}
</style>
