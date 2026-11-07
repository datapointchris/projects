<template>
  <AddEditModal
    :visible="visible"
    :focus-ref="titleInput"
    @close="handleModalClose">
    <template #default="{ handleClose, handleSuccess }">
      <form
        class="add-edit-modal__form"
        @submit.prevent="handleSubmit(handleSuccess)">
        <h2>{{ editData ? 'Edit Item' : 'Add Item' }}</h2>

        <div class="add-edit-modal__form-item">
          <label for="item-title">Title</label>
          <input
            id="item-title"
            ref="titleInput"
            v-model="form.title"
            data-testid="project-item-title-input"
            type="text"
            size="40"
            class="textbox"
            required />
        </div>

        <div class="add-edit-modal__form-item">
          <label for="item-notes">Notes</label>
          <textarea
            id="item-notes"
            v-model="form.notes"
            data-testid="project-item-notes-input"
            rows="8"
            class="textbox item-modal__notes-input"></textarea>
        </div>

        <div class="add-edit-modal__form-buttons">
          <button
            type="submit"
            data-testid="project-item-submit-button"
            class="button">
            <span class="button__text">{{ editData ? 'Update' : 'Add' }} Item</span>
          </button>
          <button
            type="button"
            data-testid="project-item-cancel-button"
            class="button button--danger"
            @click="handleClose()">
            <span class="button__text button__text--danger">Cancel</span>
          </button>
        </div>
      </form>
    </template>
  </AddEditModal>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import type { ProjectItemCreate, ProjectItemUpdate } from '@/api/client'
import AddEditModal from '@/components/AddEditModal.vue'

const props = defineProps<{
  visible: boolean
  editData?: { id: string; title: string; notes?: string } | null
  projectId: string | null
}>()

const emit = defineEmits<{
  close: []
  create: [data: ProjectItemCreate]
  update: [id: string, data: ProjectItemUpdate]
}>()

const titleInput = ref<HTMLInputElement | null>(null)

const form = reactive({
  title: '',
  notes: '',
})

watch(
  () => props.visible,
  (val) => {
    if (val && props.editData) {
      form.title = props.editData.title
      form.notes = props.editData.notes ?? ''
    }
  },
)

function resetForm() {
  form.title = ''
  form.notes = ''
}

function handleModalClose() {
  resetForm()
  emit('close')
}

function handleSubmit(handleSuccess: () => void) {
  if (!form.title.trim()) return
  if (props.editData) {
    emit('update', props.editData.id, {
      title: form.title.trim(),
      notes: form.notes.trim() || null,
    })
  } else {
    if (props.projectId === null) return
    emit('create', {
      title: form.title.trim(),
      notes: form.notes.trim() || undefined,
      project_ids: [props.projectId],
    })
  }
  handleSuccess()
}
</script>

<style scoped lang="scss">
.item-modal__notes-input {
  min-width: 400px;
  resize: vertical;
}
</style>
