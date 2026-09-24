import { useEffect, useMemo, useState } from 'react'
import {
  Table,
  Card,
  Button,
  Input,
  Modal,
  Form,
  Space,
  Popconfirm,
  message,
  Typography,
  Select,
  DatePicker,
  InputNumber,
  Tabs,
  Radio,
  Tag,
  Alert,
  Checkbox,
  Statistic,
  Row,
  Col,
} from 'antd'
import { PlusOutlined, EditOutlined, DeleteOutlined, ReloadOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import {
  paymentApi,
  studentApi,
  courseApi,
  studentCourseApi,
  type CourseAccount,
} from '@/services/api'

const { Title } = Typography
const { Option } = Select
const { TextArea } = Input

const paymentMethods = [
  { value: 'cash', label: '现金' },
  { value: 'wechat', label: '微信' },
  { value: 'alipay', label: '支付宝' },
  { value: 'bank', label: '银行转账' },
]

const paymentTypes = [
  { value: 'tuition', label: '学费' },
  { value: 'deposit', label: '定金' },
  { value: 'other', label: '其他' },
]

// 剩余课时不超过该值标记为待续费，需与后端 LowBalanceThreshold 保持一致
const LOW_BALANCE_THRESHOLD = 5

function Finance() {
  const [activeTab, setActiveTab] = useState('payments')
  const [loading, setLoading] = useState(false)
  const [payments, setPayments] = useState<any[]>([])
  const [students, setStudents] = useState<any[]>([])
  const [courses, setCourses] = useState<any[]>([])
  const [modalVisible, setModalVisible] = useState(false)
  const [modalType, setModalType] = useState<'create' | 'edit'>('create')
  const [selectedPayment, setSelectedPayment] = useState<any>(null)
  const [form] = Form.useForm()

  const [accounts, setAccounts] = useState<CourseAccount[]>([])
  const [accountLoading, setAccountLoading] = useState(false)
  const [accountKeyword, setAccountKeyword] = useState('')
  const [onlyNeedRenew, setOnlyNeedRenew] = useState(false)
  const [renewVisible, setRenewVisible] = useState(false)
  const [renewSubmitting, setRenewSubmitting] = useState(false)
  const [renewForm] = Form.useForm()
  const selectedCourseId = Form.useWatch('course_id', renewForm)
  const renewHours = Form.useWatch('hours', renewForm)

  const fetchPayments = async () => {
    try {
      setLoading(true)
      const res: any = await paymentApi.list()
      setPayments(res.list || [])
    } catch (error) {
      console.error('Fetch payments error:', error)
    } finally {
      setLoading(false)
    }
  }

  const fetchAccounts = async () => {
    try {
      setAccountLoading(true)
      const res = await studentCourseApi.accounts({
        keyword: accountKeyword || undefined,
        need_renew: onlyNeedRenew ? 1 : undefined,
      })
      setAccounts(res.list || [])
    } catch (error) {
      console.error('Fetch accounts error:', error)
    } finally {
      setAccountLoading(false)
    }
  }

  const fetchOptions = async () => {
    try {
      const [studentsRes, coursesRes] = await Promise.all([
        studentApi.list({ page_size: 1000 }),
        courseApi.list({ page_size: 1000 }),
      ])
      setStudents((studentsRes as any)?.list || [])
      setCourses((coursesRes as any)?.list || [])
    } catch (error) {
      console.error('Fetch options error:', error)
    }
  }

  useEffect(() => {
    fetchOptions()
    fetchPayments()
  }, [])

  useEffect(() => {
    if (activeTab === 'accounts') {
      fetchAccounts()
    }
  }, [activeTab])

  const renewAmount = useMemo(() => {
    const course = courses.find((c) => c.id === selectedCourseId)
    if (!course || !renewHours || renewHours <= 0) return 0
    return Math.round(course.price_per_hour * renewHours * 100) / 100
  }, [courses, selectedCourseId, renewHours])

  const lowBalanceCount = accounts.filter((a) => a.need_renew).length

  const handleCreate = () => {
    setModalType('create')
    setSelectedPayment(null)
    form.resetFields()
    form.setFieldsValue({
      payment_date: dayjs(),
      payment_method: 'wechat',
      type: 'tuition',
    })
    setModalVisible(true)
  }

  const handleEdit = (payment: any) => {
    setModalType('edit')
    setSelectedPayment(payment)
    form.setFieldsValue({
      ...payment,
      payment_date: payment.payment_date ? dayjs(payment.payment_date) : undefined,
    })
    setModalVisible(true)
  }

  const handleDelete = async (id: number) => {
    try {
      await paymentApi.delete(id)
      message.success('删除成功')
      fetchPayments()
    } catch (error) {
      console.error('Delete payment error:', error)
    }
  }

  const handleModalSubmit = async () => {
    try {
      const values = await form.validateFields()
      const data = {
        ...values,
        payment_date: values.payment_date.format('YYYY-MM-DD'),
      }

      if (modalType === 'create') {
        await paymentApi.create(data)
        message.success('创建成功')
      } else if (selectedPayment?.id) {
        await paymentApi.update(selectedPayment.id, data)
        message.success('更新成功')
      }
      setModalVisible(false)
      fetchPayments()
    } catch (error) {
      console.error('Modal submit error:', error)
    }
  }

  const openRenew = (record?: CourseAccount) => {
    renewForm.resetFields()
    renewForm.setFieldsValue({
      payment_method: 'wechat',
      payment_date: dayjs(),
      hours: 1,
      student_id: record?.student_id,
      course_id: record?.course_id,
    })
    setRenewVisible(true)
  }

  const handleRenewSubmit = async () => {
    try {
      const values = await renewForm.validateFields()
      setRenewSubmitting(true)
      await paymentApi.renew({
        student_id: values.student_id,
        course_id: values.course_id,
        hours: values.hours,
        payment_method: values.payment_method,
        payment_date: values.payment_date.format('YYYY-MM-DD'),
        remarks: values.remarks,
      })
      message.success(`续费成功，已增加 ${values.hours} 课时`)
      setRenewVisible(false)
      // 续费同时生成了缴费记录，两个视图都刷新
      fetchAccounts()
      fetchPayments()
    } catch (error) {
      // 后端事务保证失败时缴费记录与课时账户都不变，弹窗保留以便修改
      console.error('Renew error:', error)
    } finally {
      setRenewSubmitting(false)
    }
  }

  const columns = [
    {
      title: '学员',
      dataIndex: ['student', 'name'],
      key: 'student',
      render: (name: string) => name || '-',
    },
    {
      title: '课程',
      dataIndex: ['course', 'name'],
      key: 'course',
      render: (name: string) => name || '-',
    },
    {
      title: '金额(元)',
      dataIndex: 'amount',
      key: 'amount',
    },
    {
      title: '支付方式',
      dataIndex: 'payment_method',
      key: 'payment_method',
      render: (method: string) => {
        const opt = paymentMethods.find((o) => o.value === method)
        return opt?.label || method
      },
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      render: (type: string) => {
        const opt = paymentTypes.find((o) => o.value === type)
        return opt?.label || type
      },
    },
    {
      title: '日期',
      dataIndex: 'payment_date',
      key: 'payment_date',
    },
    {
      title: '收据号',
      dataIndex: 'receipt_no',
      key: 'receipt_no',
    },
    {
      title: '操作',
      key: 'action',
      render: (_: any, record: any) => (
        <Space size="small">
          <Button type="link" size="small" onClick={() => handleEdit(record)}>
            <EditOutlined /> 编辑
          </Button>
          <Popconfirm
            title="确定删除?"
            onConfirm={() => handleDelete(record.id!)}
            okText="确定"
            cancelText="取消"
          >
            <Button type="link" size="small" danger>
              <DeleteOutlined /> 删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  const accountColumns = [
    {
      title: '学员',
      dataIndex: 'student_name',
      key: 'student_name',
    },
    {
      title: '课程',
      dataIndex: 'course_name',
      key: 'course_name',
    },
    {
      title: '单价(元/小时)',
      dataIndex: 'price_per_hour',
      key: 'price_per_hour',
    },
    {
      title: '总课时',
      dataIndex: 'total_hours',
      key: 'total_hours',
    },
    {
      title: '已用课时',
      dataIndex: 'used_hours',
      key: 'used_hours',
    },
    {
      title: '剩余课时',
      dataIndex: 'remaining_hours',
      key: 'remaining_hours',
      render: (hours: number) => (
        <span style={{ fontWeight: hours <= LOW_BALANCE_THRESHOLD ? 600 : 400 }}>
          {hours}
        </span>
      ),
    },
    {
      title: '状态',
      dataIndex: 'need_renew',
      key: 'need_renew',
      render: (needRenew: boolean) =>
        needRenew ? <Tag color="orange">待续费</Tag> : <Tag color="green">正常</Tag>,
    },
    {
      title: '操作',
      key: 'action',
      render: (_: any, record: CourseAccount) => (
        <Button type="link" size="small" onClick={() => openRenew(record)}>
          <ReloadOutlined /> 续费
        </Button>
      ),
    },
  ]

  return (
    <div>
      <Title level={3} style={{ marginBottom: 24 }}>
        财务管理
      </Title>

      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: 'payments',
            label: '缴费记录',
            children: (
              <Card>
                <div
                  style={{
                    marginBottom: 16,
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                  }}
                >
                  <Alert
                    style={{ padding: '2px 12px' }}
                    type="info"
                    showIcon
                    message="普通缴费只生成缴费记录；为学员增加课时请使用「课时账户」中的续费"
                  />
                  <Button type="primary" icon={<PlusOutlined />} onClick={handleCreate}>
                    新增缴费
                  </Button>
                </div>

                <Table
                  columns={columns}
                  dataSource={payments}
                  rowKey="id"
                  loading={loading}
                />
              </Card>
            ),
          },
          {
            key: 'accounts',
            label: (
              <span>
                课时账户
                {lowBalanceCount > 0 && (
                  <Tag color="orange" style={{ marginLeft: 6 }}>
                    {lowBalanceCount} 人待续费
                  </Tag>
                )}
              </span>
            ),
            children: (
              <Card>
                <Row gutter={16} style={{ marginBottom: 16 }}>
                  <Col span={6}>
                    <Statistic title="账户总数" value={accounts.length} />
                  </Col>
                  <Col span={6}>
                    <Statistic
                      title="待续费账户"
                      value={lowBalanceCount}
                      valueStyle={{ color: lowBalanceCount > 0 ? '#fa8c16' : undefined }}
                    />
                  </Col>
                  <Col span={12} style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
                    <Input.Search
                      placeholder="搜索学员姓名/手机号"
                      allowClear
                      style={{ width: 240 }}
                      value={accountKeyword}
                      onChange={(e) => setAccountKeyword(e.target.value)}
                      onSearch={fetchAccounts}
                    />
                    <Checkbox
                      checked={onlyNeedRenew}
                      onChange={(e) => {
                        setOnlyNeedRenew(e.target.checked)
                        setTimeout(fetchAccounts, 0)
                      }}
                    >
                      只看待续费
                    </Checkbox>
                    <Button type="primary" icon={<PlusOutlined />} onClick={() => openRenew()}>
                      学员续费
                    </Button>
                  </Col>
                </Row>

                <Table
                  columns={accountColumns}
                  dataSource={accounts}
                  rowKey="id"
                  loading={accountLoading}
                  rowClassName={(record) =>
                    record.need_renew ? 'account-row-warning' : ''
                  }
                  pagination={{ pageSize: 10, showSizeChanger: true }}
                />
              </Card>
            ),
          },
        ]}
      />

      <Modal
        title={modalType === 'create' ? '新增缴费' : '编辑缴费'}
        open={modalVisible}
        onOk={handleModalSubmit}
        onCancel={() => setModalVisible(false)}
        destroyOnClose
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="student_id"
            label="学员"
            rules={[{ required: true, message: '请选择学员' }]}
          >
            <Select placeholder="请选择学员">
              {students.map((s) => (
                <Option key={s.id} value={s.id}>
                  {s.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item name="course_id" label="课程">
            <Select placeholder="请选择课程" allowClear>
              {courses.map((c) => (
                <Option key={c.id} value={c.id}>
                  {c.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="amount"
            label="金额(元)"
            rules={[{ required: true, message: '请输入金额' }]}
          >
            <InputNumber
              style={{ width: '100%' }}
              min={0}
              precision={2}
              placeholder="请输入金额"
            />
          </Form.Item>
          <Form.Item
            name="payment_method"
            label="支付方式"
            rules={[{ required: true, message: '请选择支付方式' }]}
          >
            <Radio.Group>
              {paymentMethods.map((m) => (
                <Radio key={m.value} value={m.value}>
                  {m.label}
                </Radio>
              ))}
            </Radio.Group>
          </Form.Item>
          <Form.Item
            name="type"
            label="类型"
            rules={[{ required: true, message: '请选择类型' }]}
          >
            <Select placeholder="请选择类型">
              {paymentTypes.map((t) => (
                <Option key={t.value} value={t.value}>
                  {t.label}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="payment_date"
            label="缴费日期"
            rules={[{ required: true, message: '请选择日期' }]}
          >
            <DatePicker style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="remarks" label="备注">
            <TextArea rows={2} placeholder="请输入备注" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="学员续费"
        open={renewVisible}
        onOk={handleRenewSubmit}
        onCancel={() => setRenewVisible(false)}
        confirmLoading={renewSubmitting}
        okText="确认续费"
        cancelText="取消"
        destroyOnClose
      >
        <Alert
          style={{ marginBottom: 16 }}
          type="info"
          showIcon
          message="按课程单价自动计算学费金额，生成缴费记录的同时增加账户总课时"
        />
        <Form form={renewForm} layout="vertical">
          <Form.Item
            name="student_id"
            label="学员"
            rules={[{ required: true, message: '请选择学员' }]}
          >
            <Select
              placeholder="请选择学员"
              showSearch
              optionFilterProp="children"
            >
              {students.map((s) => (
                <Option key={s.id} value={s.id}>
                  {s.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="course_id"
            label="课程"
            rules={[{ required: true, message: '请选择课程' }]}
          >
            <Select placeholder="请选择课程">
              {courses.map((c) => (
                <Option key={c.id} value={c.id}>
                  {c.name}（{c.price_per_hour} 元/小时）
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="hours"
            label="续费小时数"
            rules={[
              { required: true, message: '请输入续费小时数' },
              {
                validator: (_, value) =>
                  value > 0
                    ? Promise.resolve()
                    : Promise.reject(new Error('续费小时数必须大于0')),
              },
            ]}
          >
            <InputNumber style={{ width: '100%' }} min={1} precision={0} step={1} />
          </Form.Item>
          <Form.Item
            name="payment_method"
            label="收款方式"
            rules={[{ required: true, message: '请选择收款方式' }]}
          >
            <Radio.Group>
              {paymentMethods.map((m) => (
                <Radio key={m.value} value={m.value}>
                  {m.label}
                </Radio>
              ))}
            </Radio.Group>
          </Form.Item>
          <Form.Item label="应收金额（自动计算）">
            <span style={{ fontSize: 20, fontWeight: 600, color: '#cf1322' }}>
              ¥ {renewAmount.toFixed(2)}
            </span>
          </Form.Item>
          <Form.Item name="payment_date" label="缴费日期">
            <DatePicker style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="remarks" label="备注">
            <TextArea rows={2} placeholder="请输入备注" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default Finance
